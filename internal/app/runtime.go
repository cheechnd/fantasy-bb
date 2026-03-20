package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

type ExecutionOptions struct {
	DryRun              bool `json:"dry_run"`
	RequireConfirmation bool `json:"require_confirmation"`
}

type Printer struct {
	Out  io.Writer
	JSON bool
}

func (p Printer) Print(v any) error {
	if p.JSON {
		enc := json.NewEncoder(p.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	_, err := fmt.Fprint(p.Out, v)
	return err
}

func (p Printer) Println(v any) error {
	if p.JSON {
		enc := json.NewEncoder(p.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	_, err := fmt.Fprintln(p.Out, v)
	return err
}

func (p Printer) PrintAction(prefix string, msg string, opts ExecutionOptions) error {
	if p.JSON {
		return p.Println(map[string]any{
			"action":               prefix,
			"message":              msg,
			"dry_run":              opts.DryRun,
			"require_confirmation": opts.RequireConfirmation,
		})
	}
	mode := "APPLY"
	if opts.DryRun {
		mode = "DRY-RUN"
	}
	_, err := fmt.Fprintf(p.Out, "[%s] %s: %s\n", mode, prefix, msg)
	return err
}

func Check(ctx context.Context, name string, fn func(context.Context) error) CheckResult {
	result := CheckResult{Name: name, Status: "ok"}
	if err := fn(ctx); err != nil {
		result.Status = "fail"
		result.Error = err.Error()
	}
	return result
}

type CheckResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}
