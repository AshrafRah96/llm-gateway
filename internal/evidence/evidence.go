// Package evidence records the runtime and source behind opt-in validation runs.
package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/redis/go-redis/v9"
)

type Provenance struct {
	SourceCommit       string `json:"source_commit"`
	SourceDirty        bool   `json:"source_dirty"`
	Runtime            string `json:"runtime"`
	CPUs               int    `json:"logical_cpus"`
	RedisVersion       string `json:"redis_version"`
	RedisSearchVersion string `json:"redis_search_version"`
}

func Capture(ctx context.Context, client *redis.Client) (Provenance, error) {
	commit, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		return Provenance{}, fmt.Errorf("read source commit: %w", err)
	}
	// Generated reports and documentation changes do not change the measured code.
	dirty, err := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=all", "--", "*.go", "go.mod", "go.sum").Output()
	if err != nil {
		return Provenance{}, fmt.Errorf("read source status: %w", err)
	}
	info, err := client.Info(ctx, "server", "modules").Result()
	if err != nil {
		return Provenance{}, fmt.Errorf("read Redis versions: %w", err)
	}
	p := Provenance{
		SourceCommit: strings.TrimSpace(string(commit)), SourceDirty: len(dirty) != 0,
		Runtime: runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
		CPUs:    runtime.NumCPU(),
	}
	for _, line := range strings.Split(info, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "redis_version:"); ok {
			p.RedisVersion = value
		}
		if strings.HasPrefix(line, "module:name=search,") {
			for _, field := range strings.Split(strings.TrimSpace(line), ",") {
				if value, ok := strings.CutPrefix(field, "ver="); ok {
					p.RedisSearchVersion = value
				}
			}
		}
	}
	if p.RedisVersion == "" || p.RedisSearchVersion == "" {
		return Provenance{}, fmt.Errorf("Redis server and Search versions are required")
	}
	return p, nil
}

func (p Provenance) Markdown() string {
	return fmt.Sprintf("| Source commit | %s |\n| Source dirty | %t |\n| Runtime | %s |\n| Logical CPUs | %d |\n| Redis version | %s |\n| Redis Search version | %s |\n",
		p.SourceCommit, p.SourceDirty, p.Runtime, p.CPUs, p.RedisVersion, p.RedisSearchVersion)
}

func Write(jsonPath, markdownPath string, report any, markdown string) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	for _, output := range []struct{ path, data string }{{jsonPath, string(encoded) + "\n"}, {markdownPath, markdown}} {
		if err := os.MkdirAll(filepath.Dir(output.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(output.path, []byte(output.data), 0o644); err != nil {
			return err
		}
	}
	return nil
}
