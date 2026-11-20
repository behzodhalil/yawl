package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/behzodhalil/yawl/internal/proto"
	"github.com/behzodhalil/yawl/internal/server/app"
	"github.com/behzodhalil/yawl/internal/server/store/db"
)

type LogAlert struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Lines   int    `json:"lines"`
	Minutes int    `json:"minutes"`
}

const (
	LogAlertsKey  = "logalerts."
	LogAlertOpen  = "logalert.open."
	logAlertQuiet = 30 * time.Minute
	logWindowMax  = 60
)

func LogAlerts(ctx context.Context, q *db.Queries, serviceID string) []LogAlert {
	var out []LogAlert
	json.Unmarshal([]byte(setting(ctx, q, LogAlertsKey+serviceID)), &out)
	return out
}

type logRule struct {
	LogAlert
	service, label string
}

type logMinute struct {
	lines int
	first string
}

func logRules(ctx context.Context, q *db.Queries, label map[string]string) ([]logRule, error) {
	rows, err := q.SettingsWithPrefix(ctx, LogAlertsKey)
	if err != nil {
		return nil, err
	}
	var out []logRule
	for _, r := range rows {
		id := r.Key[len(LogAlertsKey):]
		if label[id] == "" {
			continue
		}
		var rs []LogAlert
		json.Unmarshal([]byte(r.Value), &rs)
		for _, a := range rs {
			out = append(out, logRule{a, id, label[id]})
		}
	}
	return out, nil
}

func logWatch(rules []logRule) []proto.LogWatch {
	var out []proto.LogWatch
	for _, r := range rules {
		p, rest, _ := strings.Cut(r.label, "/")
		e, s, _ := strings.Cut(rest, "/")
		out = append(out, proto.LogWatch{Rule: r.ID, Project: p, Env: e, Service: s, Text: r.Text})
	}
	return out
}

func (w *Metrics) addLogHits(samples []proto.Sample) {
	if w.logs == nil {
		w.logs = map[string]map[int64]logMinute{}
	}
	for _, s := range samples {
		for _, h := range s.LogHits {
			if w.logs[h.Rule] == nil {
				w.logs[h.Rule] = map[int64]logMinute{}
			}
			m := w.logs[h.Rule][s.At]
			m.lines += h.Lines
			if m.first == "" {
				m.first = h.First
			}
			w.logs[h.Rule][s.At] = m
		}
	}
}

func (w *Metrics) checkLogAlerts(ctx context.Context, rules []logRule, now time.Time) error {
	q := w.St.Q
	ms := now.UnixMilli()
	live := map[string]bool{}
	for _, r := range rules {
		live[r.ID] = true
		var lines int
		var firstAt int64
		first := ""
		for at, m := range w.logs[r.ID] {
			if at <= ms-int64(r.Minutes)*time.Minute.Milliseconds() {
				continue
			}
			lines += m.lines
			if m.first != "" && (first == "" || at < firstAt) {
				first, firstAt = m.first, at
			}
		}
		if lines <= r.Lines {
			continue
		}
		key := LogAlertOpen + r.ID
		open := setting(ctx, q, key) != ""
		if err := w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.SetSetting(ctx, db.SetSettingParams{Key: key, Value: strconv.FormatInt(ms, 10) + " " + r.label}); err != nil {
				return err
			}
			if open {
				return nil
			}
			what := "Too many stderr lines"
			if r.Text != "" {
				what = fmt.Sprintf("Too many lines with %q", r.Text)
			}
			value := fmt.Sprintf("%d lines in %d minutes (limit %d)", lines, r.Minutes, r.Lines)
			return announceIncident(ctx, q, ms, "service.logs", r.service, map[string]any{"rule": r.ID, "lines": lines},
				app.LogAlert(r.label, what, value, first, w.logMessage(r, lines, first)), "logs:"+r.ID, "open", r.label, false)
		}); err != nil {
			return err
		}
	}
	for rule, m := range w.logs {
		for at := range m {
			if at <= ms-logWindowMax*time.Minute.Milliseconds() {
				delete(m, at)
			}
		}
		if len(m) == 0 || !live[rule] {
			delete(w.logs, rule)
		}
	}
	open, err := q.SettingsWithPrefix(ctx, LogAlertOpen)
	if err != nil {
		return err
	}
	for _, o := range open {
		rule := o.Key[len(LogAlertOpen):]
		v, label, _ := strings.Cut(o.Value, " ")
		last, _ := strconv.ParseInt(v, 10, 64)
		text, line := "", ""
		switch {
		case !live[rule]:
			text, line = label+": the log alert was removed.", "The log alert was removed"
		case ms-last >= logAlertQuiet.Milliseconds():
			what := w.logWhat(rules, rule)
			text, line = label+": "+what+" under the alert for 30 minutes.", strings.ToUpper(what[:1])+what[1:]+" under the alert for 30 minutes"
		default:
			continue
		}
		if err := w.St.Tx(ctx, func(q *db.Queries) error {
			if err := q.DeleteSettingKey(ctx, o.Key); err != nil {
				return err
			}
			return announceIncident(ctx, q, ms, "service.logs", rule, nil, app.LogAlertOver(label, line, text), "logs:"+rule, "close", label, false)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *Metrics) logWhat(rules []logRule, rule string) string {
	i := slices.IndexFunc(rules, func(r logRule) bool { return r.ID == rule })
	if rules[i].Text == "" {
		return "stderr lines"
	}
	return fmt.Sprintf("lines with %q", rules[i].Text)
}

func (w *Metrics) logMessage(r logRule, lines int, first string) string {
	what := fmt.Sprintf("%d stderr lines", lines)
	switch {
	case r.Text != "" && lines == 1:
		what = fmt.Sprintf("a line with %q", r.Text)
	case r.Text != "":
		what = fmt.Sprintf("%d lines with %q", lines, r.Text)
	case lines == 1:
		what = "1 stderr line"
	}
	text := fmt.Sprintf("%s: %s in %d minutes", r.label, what, r.Minutes)
	if r.Lines > 0 {
		text += fmt.Sprintf(" (alert: more than %d)", r.Lines)
	}
	text += "."
	if first != "" {
		text += "\nFirst: " + first
	}
	p, rest, _ := strings.Cut(r.label, "/")
	e, s, _ := strings.Cut(rest, "/")
	return text + "\nLogs: " + strings.TrimRight(w.PanelURL, "/") + "/p/" + p + "/" + e + "/s/" + s + "/logs"
}
