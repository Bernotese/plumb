package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/plumb/internal/api"
	"github.com/Bernotese/plumb/internal/store"
)

type outputProbe func([]byte) (int, error)

func (p outputProbe) Write(b []byte) (int, error) { return p(b) }

func TestDevUserExistsBeforeLoginLink(t *testing.T) {
	for _, seeded := range []bool{false, true} {
		t.Run(fmt.Sprintf("seeded=%v", seeded), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			data := t.TempDir()
			if err := checkMode(data, true); err != nil {
				t.Fatal(err)
			}
			st, err := store.Open(ctx, filepath.Join(data, "plumb.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if seeded {
				if _, err := st.CommitRevision(ctx, store.NewRevision{Author: "system"}); err != nil {
					t.Fatal(err)
				}
			}
			f, err := parseServe([]string{"--dev", "--dev-demo", "--data", data, "--listen", "127.0.0.1:0", "--agent-listen", "127.0.0.1:0"}, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			observed, done := make(chan error, 1), make(chan error, 1)
			out := outputProbe(func(b []byte) (int, error) {
				if strings.HasPrefix(string(b), "dev login:") {
					u, err := st.UserByName(ctx, api.DevUser)
					if err == nil && (u.PasswordHash != "!" || u.MustChangePassword) {
						err = fmt.Errorf("dev user has a password or requires a password change")
					}
					observed <- err // Observe synchronously, before Write returns and async seeding starts.
					cancel()
				}
				return len(b), nil
			})
			go func() { done <- runServer(ctx, f, out, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
			select {
			case err := <-observed:
				if err != nil {
					t.Errorf("login link preceded usable dev user: %v", err)
				}
			case <-time.After(5 * time.Second):
				cancel()
				t.Error("server did not print the login link")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
