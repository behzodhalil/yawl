package exec

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/agent/runtime"
	"github.com/behzodhalil/yawl/internal/proto"
)

const (
	queryRows    = 1000
	queryTimeout = 30 * time.Second
	queryMemory  = 256 << 20
	// pgNull is how psql prints NULL: unprintable, so empty text stays empty
	pgNull = "\x1d"
)

var queryBytes, queryJSON = 3 << 20, 3 << 20

var errFull = errors.New("the result is past the size limit")

type capped struct {
	b   bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if c.b.Len()+len(p) > c.max {
		c.b.Write(p[:c.max-c.b.Len()])
		return 0, errFull
	}
	return c.b.Write(p)
}

const pgRun = `exec psql -X -q --csv -P "null=$1" -v FETCH_COUNT=500 -v SHOW_ALL_RESULTS=off -v ON_ERROR_STOP=1 -h "$2" -U "$3" -d app -c "$sql" -c '\warn :ROW_COUNT'`

// pgReader readies yawl_read, the role a read runs as: it reads everything
// and is no superuser. app is one (POSTGRES_USER), and its read-only default
// is one SET away from writes and COPY ... PROGRAM. It
// runs as app in a client of its own; the password follows app's on every
// read: a PITR restore changes app's.
const pgReader = `exec psql -X -q -v ON_ERROR_STOP=1 -v pass="$READ" -h "$1" -U app -d app >/dev/null <<'SQL'
SELECT set_config('yawl.pass', :'pass', false);
DO $$BEGIN
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'yawl_read') THEN CREATE ROLE yawl_read LOGIN IN ROLE pg_read_all_data; END IF;
	EXECUTE format('ALTER ROLE yawl_read PASSWORD %L', current_setting('yawl.pass'));
EXCEPTION WHEN duplicate_object OR unique_violation OR internal_error THEN NULL; -- a read in parallel got there first
END$$;
SQL
`

// myReader readies yawl_read on MySQL and MariaDB, inside the database's
// container: root is localhost only, and app may not make users. Its
// password comes from the container's own environment; yawl_read's is the
// line typed in, so neither rides in argv.
const myReader = `read -r p && printf "CREATE USER IF NOT EXISTS yawl_read IDENTIFIED BY '%s';
ALTER USER yawl_read IDENTIFIED BY '%s';
GRANT SELECT, SHOW VIEW ON app.* TO yawl_read;
" "$p" "$p" | MYSQL_PWD=$(printenv "$2") "$1" -u root && echo yawl-read-ok`

// readPassword is yawl_read's password, made from app's: the client that
// runs a read's SQL holds only it, as psql's \! and mysql's system give
// that SQL a shell there.
func readPassword(pw []byte) string {
	m := hmac.New(sha256.New, pw)
	m.Write([]byte("yawl_read"))
	return hex.EncodeToString(m.Sum(nil))
}

func (d *Dispatcher) reader(ctx context.Context, a proto.QueryArgs, spec runtime.ContainerSpec, pw []byte, read string) string {
	if isPostgres(a.Kind) {
		spec.Env = []string{"PGPASSWORD=" + string(pw), "READ=" + read}
		spec.Cmd = []string{"sh", "-c", pgReader, "sh", a.Name}
		if exit, stderr, err := d.rt.RunOnce(ctx, spec, nil, nil); err != nil || exit != 0 {
			return fmt.Sprintf("Yawl could not ready its read-only user: exit %d %s %v", exit, strings.TrimSpace(stderr), err)
		}
		return ""
	}
	container, _, _, _ := dbNames(a.Kind, a.Project, a.Env, a.Name, a.Version)
	sh, err := d.rt.Exec(ctx, container, []string{"sh", "-c", myReader, "sh", a.Kind, strings.ToUpper(a.Kind) + "_ROOT_PASSWORD"}, 200, 50)
	if err != nil {
		return "Yawl could not ready its read-only user: " + err.Error()
	}
	defer sh.Close()
	defer context.AfterFunc(ctx, func() { sh.Close() })() // Docker's exec stream outlives ctx
	if _, err := io.WriteString(sh, read+"\n"); err != nil {
		return "Yawl could not ready its read-only user: " + err.Error()
	}
	out, _ := io.ReadAll(io.LimitReader(sh, 64<<10))
	if !bytes.Contains(out, []byte("yawl-read-ok")) { // a TTY has no exit status apart from its output
		return "Yawl could not ready its read-only user: " + strings.TrimSpace(strings.ReplaceAll(string(out), read, "")) // the terminal echoes the line typed
	}
	return ""
}

func (d *Dispatcher) query(ctx context.Context, a proto.QueryArgs) proto.Result {
	if !isPostgres(a.Kind) && a.Kind != "mysql" && a.Kind != "mariadb" {
		return proto.Result{Cause: "Yawl does not browse " + a.Kind + " yet."}
	}
	pw, cause := d.open(proto.SealedVar{Key: "PASSWORD", Box: a.Password})
	if cause != "" {
		return proto.Result{Cause: cause}
	}
	_, _, network, image := dbNames(a.Kind, a.Project, a.Env, a.Name, a.Version)
	spec := runtime.ContainerSpec{Image: image, Network: network, Memory: queryMemory}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout+5*time.Second)
	defer cancel()
	user, pass := "app", string(pw)
	if !a.Write {
		user, pass = "yawl_read", readPassword(pw)
		if cause := d.reader(ctx, a, spec, pw, pass); cause != "" {
			return proto.Result{Cause: cause}
		}
	}
	stdin := a.SQL
	secs := strconv.Itoa(int(queryTimeout.Seconds()))
	if isPostgres(a.Kind) {
		opts := "-c statement_timeout=" + secs + "000"
		if !a.Write {
			opts += " -c default_transaction_read_only=on"
		}
		spec.Env = []string{"PGPASSWORD=" + pass, "PGOPTIONS=" + opts}
		spec.Cmd = []string{"sh", "-c", "sql=$(cat)\n" + pgRun, "sh", pgNull, a.Name, user}
	} else {
		init := "SET SESSION max_execution_time=" + secs + "000"
		if a.Kind == "mariadb" {
			init = "SET SESSION max_statement_time=" + secs
		}
		if !a.Write {
			init += ", SESSION transaction_read_only=ON"
		}
		spec.Env = engines[a.Kind].clientEnv(a.Name, pass)
		spec.Cmd = []string{a.Kind, "--xml", "--quick", "--binary-as-hex", "--init-command=" + init, "-h", a.Name, "-u", user, "app"}
		stdin += "\n;\nSELECT ROW_COUNT() AS yawl_changed"
	}
	out := &capped{max: queryBytes}
	exit, stderr, err := d.rt.RunOnce(ctx, spec, strings.NewReader(stdin), out)
	cut := errors.Is(err, errFull)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return proto.Result{Cause: "The query ran past " + strconv.Itoa(int(queryTimeout.Seconds()+5)) + " seconds and was stopped."}
	case err != nil && !cut:
		return proto.Result{Cause: err.Error()}
	case exit != 0 && !cut:
		if i := strings.Index(stderr, "ERROR"); i > 0 { // MariaDB echoes the statement first
			stderr = stderr[i:]
		}
		return proto.Result{Cause: strings.TrimSpace(stderr)}
	}
	var data proto.QueryData
	if isPostgres(a.Kind) {
		data, err = pgResult(out.b.Bytes(), stderr, cut)
	} else {
		data, err = myResult(out.b.Bytes(), cut)
	}
	if err != nil {
		return proto.Result{Cause: "Yawl could not read the result: " + err.Error()}
	}
	return fitted(data)
}

func fitted(data proto.QueryData) proto.Result {
	for {
		b, _ := json.Marshal(data)
		if len(b) <= queryJSON || len(data.Rows) == 0 {
			return proto.Result{OK: true, Data: b}
		}
		data.Rows, data.More = data.Rows[:len(data.Rows)/2], true
	}
}

func keep(data *proto.QueryData, row []*string) {
	if len(data.Rows) < queryRows {
		data.Rows = append(data.Rows, row)
	} else {
		data.More = true
	}
}

func pgResult(out []byte, stderr string, cut bool) (proto.QueryData, error) {
	var data proto.QueryData
	if len(out) == 0 {
		if cut {
			return data, nil
		}
		lines := strings.Fields(stderr)
		if len(lines) == 0 {
			return data, errors.New("psql gave no row count")
		}
		n, err := strconv.ParseInt(lines[len(lines)-1], 10, 64)
		if err != nil {
			return data, errors.New("psql gave no row count")
		}
		data.Changed = &n
		return data, nil
	}
	r := csv.NewReader(bytes.NewReader(out))
	r.FieldsPerRecord = -1
	head, err := r.Read()
	if err != nil {
		return data, err
	}
	data.Columns, data.Rows = head, [][]*string{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			if cut {
				break
			}
			return data, err
		}
		row := make([]*string, len(rec))
		for i := range rec {
			if rec[i] != pgNull {
				row[i] = &rec[i]
			}
		}
		keep(&data, row)
	}
	if cut && len(data.Rows) > 0 && len(data.Rows) < queryRows {
		data.Rows = data.Rows[:len(data.Rows)-1]
	}
	data.More = data.More || cut
	return data, nil
}

func myResult(out []byte, cut bool) (proto.QueryData, error) {
	var sets []proto.QueryData
	var row []*string
	var names []string
	var val *string
	dec := xml.NewDecoder(bytes.NewReader(out))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			if cut {
				break
			}
			return proto.QueryData{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "resultset":
				sets = append(sets, proto.QueryData{Rows: [][]*string{}})
			case "row":
				row, names = nil, nil
			case "field":
				s := ""
				val = &s
				for _, at := range t.Attr {
					switch at.Name.Local {
					case "name":
						names = append(names, at.Value)
					case "nil":
						val = nil
					}
				}
			}
		case xml.CharData:
			if val != nil {
				*val += string(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "field":
				row, val = append(row, val), nil
			case "row":
				if len(sets) > 0 {
					set := &sets[len(sets)-1]
					if set.Columns == nil {
						set.Columns = names
					}
					keep(set, row)
				}
			}
		}
	}
	if len(sets) > 0 && !cut {
		last := sets[len(sets)-1]
		if len(last.Columns) == 1 && last.Columns[0] == "yawl_changed" && len(last.Rows) == 1 && last.Rows[0][0] != nil {
			sets = sets[:len(sets)-1]
			if n, err := strconv.ParseInt(*last.Rows[0][0], 10, 64); err == nil && n >= 0 {
				return proto.QueryData{Changed: &n}, nil
			}
		}
	}
	if len(sets) == 0 {
		return proto.QueryData{Rows: [][]*string{}, More: cut}, nil
	}
	data := sets[len(sets)-1]
	data.More = data.More || cut
	return data, nil
}
