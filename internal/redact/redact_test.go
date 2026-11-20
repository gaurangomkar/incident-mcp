package redact

import (
	"strings"
	"testing"
)

func TestApply(t *testing.T) {
	r := New(true)
	pem := "-----BEGIN PRIVATE KEY-----\nY2Nj\n-----END PRIVATE KEY-----"
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	cases := []struct {
		name string
		in   string
		want string
		keep string
	}{
		{name: "plain", in: "level=info msg=listening port=8080", want: "level=info msg=listening port=8080"},
		{name: "password", in: "db password=hunter2-secret failed", want: "db password=[REDACTED] failed"},
		{name: "short token", in: "token=true", want: "token=true"},
		{name: "quoted", in: `api_key="abcd"`, want: `api_key=[REDACTED]`},
		{name: "label", in: `{password="hunter2secret"}`, want: `{password=[REDACTED]}`},
		{name: "json", in: `{"client_secret": "supersecretvalue"}`, want: `{"client_secret": [REDACTED]}`},
		{name: "bearer", in: "Authorization: Bearer abcdefghijklmnop", want: "Authorization: Bearer [REDACTED]"},
		{name: "jwt", in: "session " + jwt, want: "session [REDACTED]"},
		{name: "pem", in: pem, want: "[REDACTED PRIVATE KEY]"},
		{name: "aws", in: "key AKIAIOSFODNN7EXAMPLE leaked", want: "key [REDACTED] leaked"},
		{name: "github", in: "ghp_" + strings.Repeat("a", 36), want: "[REDACTED]"},
		{name: "slack", in: "xoxb-1234567890-abcdefghij", want: "[REDACTED]"},
		{name: "url", in: "dial postgres://app:s3cret@db:5432/app", want: "dial postgres://[REDACTED]@db:5432/app"},
		{name: "url no user", in: "http://prometheus:9090/graph", want: "http://prometheus:9090/graph"},
		{name: "embedded word", in: "mytoken=hunter2secret", want: "mytoken=hunter2secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Apply(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDisabled(t *testing.T) {
	in := "password=hunter2secret"
	if got := New(false).Apply(in); got != in {
		t.Fatalf("got %q", got)
	}
	var r *Redactor
	if got := r.Apply(in); got != in {
		t.Fatalf("nil redactor got %q", got)
	}
}

func FuzzApply(f *testing.F) {
	f.Add("password=hunter2secret")
	f.Add("level=info ok")
	f.Add("")
	r := New(true)
	f.Fuzz(func(t *testing.T, s string) {
		out := r.Apply(s)
		if strings.Contains(s, "password=hunter2secret") && strings.Contains(out, "hunter2secret") {
			t.Fatalf("secret survived: %q", out)
		}
	})
}
