package join

import (
	"context"
	"testing"
	"time"
)

func TestJoinServerLimit(t *testing.T) {
	st, ctx := testStore(t), context.Background()
	ca, _ := LoadOrCreateCA(t.TempDir())
	h := HandlerLimited(st, ca, 2, func(string) {})
	tok := func() string { s, _ := NewToken(ctx, st.Q, time.Now()); return s }
	for i, n := range []string{"hel1", "fsn1"} {
		if code := joinAs(t, h, tok(), n, "1.2.3."+string(rune('4'+i)), wgKey(byte(i+1))); code != 200 {
			t.Fatalf("%s: %d", n, code)
		}
	}
	third := tok()
	if code := joinAs(t, h, third, "nbg1", "1.2.3.9", wgKey(9)); code != 403 {
		t.Fatalf("a third server: %d", code)
	}
	var used int
	st.DB.QueryRow(`SELECT COUNT(*) FROM join_tokens WHERE used_at IS NOT NULL`).Scan(&used)
	if used != 2 {
		t.Fatalf("the refused token was spent: %d used", used)
	}
	if code := joinAs(t, h, tok(), "hel1", "1.2.3.4", wgKey(1)); code != 200 {
		t.Fatalf("a rejoin: %d", code)
	}
}
