package hetzner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

var APIBase = "https://api.hetzner.cloud/v1"

var client = &http.Client{Timeout: 30 * time.Second}

func call(ctx context.Context, token, method, path string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		json.NewEncoder(&body).Encode(in)
	}
	req, err := http.NewRequestWithContext(ctx, method, APIBase+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Hetzner: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		var e struct{ Error struct{ Message string } }
		json.NewDecoder(res.Body).Decode(&e)
		return fmt.Errorf("Hetzner said %d: %s", res.StatusCode, e.Error.Message)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

func Check(ctx context.Context, token string) error {
	return call(ctx, token, "GET", "/locations", nil, nil)
}

type Location struct {
	Name string `json:"name"`
	City string `json:"city"`
}

type Type struct {
	Name             string            `json:"name"`
	Cores            int               `json:"cores"`
	MemoryGB         float64           `json:"memory"`
	DiskGB           int               `json:"disk"`
	Arch             string            `json:"architecture"`
	PricesByLocation map[string]string `json:"prices"`
}

type Options struct {
	Locations []Location `json:"locations"`
	Types     []Type     `json:"types"`
}

func Choices(ctx context.Context, token string) (Options, error) {
	var l struct{ Locations []Location }
	if err := call(ctx, token, "GET", "/locations", nil, &l); err != nil {
		return Options{}, err
	}
	var t struct {
		ServerTypes []struct {
			Type
			Deprecated bool `json:"deprecated"`
			Prices     []struct {
				Location     string `json:"location"`
				PriceMonthly struct {
					Gross string `json:"gross"`
				} `json:"price_monthly"`
			} `json:"prices"`
		} `json:"server_types"`
	}
	if err := call(ctx, token, "GET", "/server_types?per_page=50", nil, &t); err != nil {
		return Options{}, err
	}
	out := Options{Locations: l.Locations, Types: []Type{}}
	for _, s := range t.ServerTypes {
		if s.Deprecated {
			continue
		}
		s.Type.PricesByLocation = map[string]string{}
		for _, p := range s.Prices {
			f, _ := strconv.ParseFloat(p.PriceMonthly.Gross, 64)
			s.Type.PricesByLocation[p.Location] = strconv.FormatFloat(f, 'f', 2, 64)
		}
		out.Types = append(out.Types, s.Type)
	}
	return out, nil
}

type Server struct {
	ID int64  `json:"id"`
	IP string `json:"ip"`
}

func Create(ctx context.Context, token, name, typ, location, userData string) (Server, error) {
	var k struct {
		SSHKeys []struct{ ID int64 } `json:"ssh_keys"`
	}
	if err := call(ctx, token, "GET", "/ssh_keys", nil, &k); err != nil {
		return Server{}, err
	}
	keys := []int64{}
	for _, x := range k.SSHKeys {
		keys = append(keys, x.ID)
	}
	var s struct {
		Server struct {
			ID        int64
			PublicNet struct {
				IPv4 struct{ IP string } `json:"ipv4"`
			} `json:"public_net"`
		}
	}
	err := call(ctx, token, "POST", "/servers", map[string]any{"name": name, "server_type": typ, "location": location,
		"image": "ubuntu-24.04", "ssh_keys": keys, "user_data": userData, "labels": map[string]string{"yawl": "1"}}, &s)
	return Server{ID: s.Server.ID, IP: s.Server.PublicNet.IPv4.IP}, err
}
