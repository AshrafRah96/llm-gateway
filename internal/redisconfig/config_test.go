package redisconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConnectionValidation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(file, []byte("secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		env   map[string]string
		valid bool
	}{
		{map[string]string{}, true},
		{map[string]string{"REDIS_PASSWORD_FILE": file, "REDIS_USERNAME": "runtime", "REDIS_TLS": "true"}, true},
		{map[string]string{"REDIS_TLS": "yes"}, false},
		{map[string]string{"REDIS_TLS_CA": "ca"}, false},
		{map[string]string{"REDIS_PASSWORD": "a", "REDIS_PASSWORD_FILE": file}, false},
		{map[string]string{"REDIS_TLS": "true", "REDIS_TLS_CERT": "cert"}, false},
	} {
		o, err := Options(func(k string) string { return tt.env[k] }, "REDIS_", "localhost:6379")
		if (err == nil) != tt.valid {
			t.Fatalf("%v: %v", tt.env, err)
		}
		if err == nil && o.TLSConfig != nil && o.TLSConfig.InsecureSkipVerify {
			t.Fatal("unverified TLS")
		}
		if err == nil && tt.env["REDIS_PASSWORD_FILE"] != "" && o.Password != "secret" {
			t.Fatal("secret file not read")
		}
	}
}
