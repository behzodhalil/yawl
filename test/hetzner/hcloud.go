package hetzner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const API = "https://api.hetzner.cloud/v1"

type Client struct {
	Base, Token string
	HTTP        *http.Client
}

type ServerOpts struct{ Name, Type, Image, Location, PublicKey string }

type Server struct {
	ID int64
	IP string
}

func (c Client) call(ctx context.Context, method, path string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		json.NewEncoder(&body).Encode(in)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: time.Minute}
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		var e struct{ Error struct{ Message string } }
		json.NewDecoder(res.Body).Decode(&e)
		return fmt.Errorf("hetzner %s %s: %d %s", method, path, res.StatusCode, e.Error.Message)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

type serverJSON struct {
	Server struct {
		ID        int64
		Status    string
		PublicNet struct {
			IPv4 struct{ IP string } `json:"ipv4"`
		} `json:"public_net"`
	}
}

type Cleaner interface {
	Cleanup(func())
	Errorf(format string, args ...any)
}

func Provision(ctx context.Context, c Client, cl Cleaner, o ServerOpts) (Server, error) {
	var key struct {
		SSHKey struct{ ID int64 } `json:"ssh_key"`
	}
	if err := c.call(ctx, "POST", "/ssh_keys", map[string]string{"name": o.Name, "public_key": o.PublicKey}, &key); err != nil {
		return Server{}, err
	}
	cl.Cleanup(func() {
		if err := c.call(context.Background(), "DELETE", fmt.Sprintf("/ssh_keys/%d", key.SSHKey.ID), nil, nil); err != nil {
			cl.Errorf("could not delete SSH key %d (%s): %v. Delete it in the Hetzner console.", key.SSHKey.ID, o.Name, err)
		}
	})
	var s serverJSON
	err := c.call(ctx, "POST", "/servers", map[string]any{"name": o.Name, "server_type": o.Type, "image": o.Image,
		"location": o.Location, "ssh_keys": []int64{key.SSHKey.ID},
		"labels": map[string]string{"yawl-accept": "1"}}, &s)
	if err != nil {
		return Server{}, err
	}
	id := s.Server.ID
	cl.Cleanup(func() {
		if err := c.call(context.Background(), "DELETE", fmt.Sprintf("/servers/%d", id), nil, nil); err != nil {
			cl.Errorf("could not delete server %d (%s): %v. It keeps billing: delete it in the Hetzner console.", id, o.Name, err)
		}
	})
	for s.Server.Status != "running" {
		select {
		case <-ctx.Done():
			return Server{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		if err := c.call(ctx, "GET", fmt.Sprintf("/servers/%d", id), nil, &s); err != nil {
			return Server{}, err
		}
	}
	return Server{ID: id, IP: s.Server.PublicNet.IPv4.IP}, nil
}
