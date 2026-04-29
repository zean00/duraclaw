package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"duraclaw/internal/agentconfig"
	"duraclaw/internal/db"
)

func runAgentConfigCLI(ctx context.Context, args []string) (bool, error) {
	if len(args) == 0 || args[0] != "agent-config" {
		return false, nil
	}
	if len(args) < 2 {
		return true, fmt.Errorf("usage: duraclaw agent-config <import|export> [flags]")
	}
	cfg, err := loadConfig()
	if err != nil {
		return true, err
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return true, err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return true, err
	}
	store := db.NewStore(pool)
	switch args[1] {
	case "import":
		return true, runAgentConfigImport(ctx, store, args[2:])
	case "export":
		return true, runAgentConfigExport(ctx, store, args[2:])
	default:
		return true, fmt.Errorf("unknown agent-config command %q", args[1])
	}
}

func runAgentConfigImport(ctx context.Context, store *db.Store, args []string) error {
	opts := parseAgentConfigFlags(args)
	if opts["file"] == "" {
		return fmt.Errorf("agent-config import requires --file")
	}
	format := opts["format"]
	if format == "" {
		format = formatFromPath(opts["file"])
	}
	f, err := os.Open(opts["file"])
	if err != nil {
		return err
	}
	defer f.Close()
	doc, err := agentconfig.Decode(f, format)
	if err != nil {
		return err
	}
	if opts["customer-id"] != "" {
		doc.CustomerID = opts["customer-id"]
	}
	if opts["agent-instance-id"] != "" {
		doc.AgentInstanceID = opts["agent-instance-id"]
	}
	if opts["activate"] == "true" || opts["activate"] == "1" {
		doc.ActivateImmediately = true
	}
	version, err := agentconfig.Import(ctx, store, doc)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "imported agent_instance_id=%s version_id=%s version=%d\n", version.AgentInstanceID, version.ID, version.Version)
	return nil
}

func runAgentConfigExport(ctx context.Context, store *db.Store, args []string) error {
	opts := parseAgentConfigFlags(args)
	if opts["version-id"] == "" {
		return fmt.Errorf("agent-config export requires --version-id")
	}
	format := opts["format"]
	if format == "" {
		format = formatFromPath(opts["file"])
	}
	if format == "" {
		format = "yaml"
	}
	doc, err := agentconfig.Export(ctx, store, opts["version-id"])
	if err != nil {
		return err
	}
	raw, err := agentconfig.Encode(doc, format)
	if err != nil {
		return err
	}
	var out io.Writer = os.Stdout
	var f *os.File
	if opts["file"] != "" {
		f, err = os.Create(opts["file"])
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}
	_, err = out.Write(append(raw, '\n'))
	return err
}

func parseAgentConfigFlags(args []string) map[string]string {
	opts := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		key := strings.TrimPrefix(arg, "--")
		if key == "activate" {
			opts[key] = "true"
			continue
		}
		if name, value, ok := strings.Cut(key, "="); ok {
			opts[name] = value
			continue
		}
		if i+1 < len(args) {
			opts[key] = args[i+1]
			i++
		}
	}
	return opts
}

func formatFromPath(path string) string {
	lower := strings.ToLower(strings.TrimSpace(path))
	switch {
	case strings.HasSuffix(lower, ".yaml"), strings.HasSuffix(lower, ".yml"):
		return "yaml"
	case strings.HasSuffix(lower, ".json"):
		return "json"
	default:
		return ""
	}
}
