package cloud

import (
	"strings"
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{
		"YAWL_CLOUD_PUBLIC_URL": "https://cloud.198-51-100-20.sslip.io", "YAWL_CLOUD_CELL_HOST": "panel-{name}.198-51-100-20.sslip.io",
		"YAWL_CLOUD_IMAGE": "registry.test/yawl:0.1.0", "YAWL_CLOUD_GITHUB_ID": "id", "YAWL_CLOUD_GITHUB_SECRET": "sec",
		"YAWL_CLOUD_ADMIN_TOKEN": "adm", "YAWL_CLOUD_OPERATOR_URL": "https://panel.198-51-100-20.sslip.io", "YAWL_CLOUD_OPERATOR_TOKEN": "op",
		"YAWL_CLOUD_SERVER": "srv1", "YAWL_CLOUD_SERVERS": "srv1:50@203.0.113.1", "YAWL_CLOUD_INVITES": "a, b", "YAWL_CLOUD_INVITE_ONLY": "1"}
	cfg, listen, err := configFrom(nil, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	op, _ := cfg.Cells.(Operator)
	if listen != "0.0.0.0:7000" || cfg.InstallURL != "https://cloud.198-51-100-20.sslip.io/install.sh" || cfg.Releases != "https://cloud.198-51-100-20.sslip.io/releases" ||
		cfg.MaxCells != 50 || !cfg.InviteOnly || strings.Join(cfg.Invites, ",") != "a,b" || cfg.GitHubWeb != "https://github.com" || cfg.GitHubAPI != "https://api.github.com" ||
		op.Project != "cells" || op.Server != "srv1" || op.Token != "op" || cfg.Data != "/data" {
		t.Fatalf("%+v %s", cfg, listen)
	}
	delete(env, "YAWL_CLOUD_GITHUB_SECRET")
	if _, _, err := configFrom(nil, func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "YAWL_CLOUD_GITHUB_SECRET") {
		t.Fatalf("%v", err)
	}
	env["YAWL_CLOUD_GITHUB_SECRET"], env["YAWL_CLOUD_CELL_HOST"] = "sec", "acme.example"
	if _, _, err := configFrom(nil, func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "{name}") {
		t.Fatalf("%v", err)
	}
}

func TestConfigDodo(t *testing.T) {
	env := map[string]string{
		"YAWL_CLOUD_PUBLIC_URL": "https://cloud.test", "YAWL_CLOUD_CELL_HOST": "panel-{name}.test", "YAWL_CLOUD_IMAGE": "registry.test/yawl:0.1.0",
		"YAWL_CLOUD_GITHUB_ID": "id", "YAWL_CLOUD_GITHUB_SECRET": "sec", "YAWL_CLOUD_ADMIN_TOKEN": "adm", "YAWL_CLOUD_OPERATOR_URL": "https://op.test",
		"YAWL_CLOUD_OPERATOR_TOKEN": "op", "YAWL_CLOUD_SERVERS": "srv:50@203.0.113.1"}
	get := func(k string) string { return env[k] }
	if cfg, _, err := configFrom(nil, get); err != nil || cfg.DodoKey != "" {
		t.Fatalf("billing off: %v", err)
	}
	env["YAWL_CLOUD_DODO_API_KEY"] = "key"
	if _, _, err := configFrom(nil, get); err == nil || !strings.Contains(err.Error(), "YAWL_CLOUD_DODO_WEBHOOK_SECRET") || !strings.Contains(err.Error(), "YAWL_CLOUD_DODO_PRODUCT") {
		t.Fatalf("%v", err)
	}
	env["YAWL_CLOUD_DODO_WEBHOOK_SECRET"], env["YAWL_CLOUD_DODO_PRODUCT"] = "whsec_c2VjcmV0", "pdt_1"
	cfg, _, err := configFrom(nil, get)
	if err != nil || cfg.DodoAPI != "https://test.dodopayments.com" || cfg.Price != "$3.99 a month" || cfg.DodoProduct != "pdt_1" {
		t.Fatalf("%+v %v", cfg, err)
	}
	env["YAWL_CLOUD_DODO_MODE"], env["YAWL_CLOUD_PRICE"] = "live", "$4.99 a month"
	if cfg, _, _ := configFrom(nil, get); cfg.DodoAPI != "https://live.dodopayments.com" || cfg.Price != "$4.99 a month" {
		t.Fatalf("%+v", cfg)
	}
	env["YAWL_CLOUD_DODO_MODE"] = "prod"
	if _, _, err := configFrom(nil, get); err == nil || !strings.Contains(err.Error(), "YAWL_CLOUD_DODO_MODE") {
		t.Fatalf("%v", err)
	}
}
