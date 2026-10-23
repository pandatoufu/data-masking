package desktop

import (
	"errors"
	"testing"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

func TestStartupPreflightBusFailure(t *testing.T) {
	app := NewApp(nil, nil)
	app.SetSessionBusCheck(func() error { return errors.New("no bus") })
	var notified string
	quit := false
	ok := app.RunStartupPreflight(func(msg string) { notified = msg }, func() { quit = true })
	if ok {
		t.Fatal("bus 不可用应返回 false")
	}
	if quit == false {
		t.Fatal("bus 不可用应 Quit")
	}
	want := i18n.T(app.Language(), "native.session_bus_unavailable", nil)
	if notified != want || !contains(notified, "session bus") {
		t.Fatalf("应给当前语言 session bus 原因：%q want %q", notified, want)
	}
}

func TestStartupPreflightBusOK(t *testing.T) {
	app := NewApp(nil, nil)
	app.SetSessionBusCheck(func() error { return nil })
	if !app.RunStartupPreflight(nil, nil) {
		t.Fatal("bus 可用应返回 true")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
