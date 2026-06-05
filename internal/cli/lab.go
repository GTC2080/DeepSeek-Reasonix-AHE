package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/harness"
	"reasonix/internal/i18n"
	"reasonix/internal/lab"
	"reasonix/internal/trace"
)

func labCommand(args []string) int {
	if len(args) == 0 {
		labUsage()
		return 2
	}
	switch args[0] {
	case "harness":
		return labHarnessCommand(args[1:])
	case "eval":
		return labEvalCommand(args[1:])
	case "cache-report":
		return labCacheReportCommand(args[1:])
	case "distill":
		return labDistillCommand(args[1:])
	case "proposal":
		return labProposalCommand(args[1:])
	case "gc":
		return labGCCommand(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown lab command %q\n", args[0])
		labUsage()
		return 2
	}
}

func labGCCommand(args []string) int {
	if len(args) != 1 || args[0] != "--dry-run" {
		labGCUsage()
		return 2
	}
	report, err := lab.PlanGC(lab.GCOptions{DryRun: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}
	fmt.Print(lab.FormatGCReport(report))
	return 0
}

func labDistillCommand(args []string) int {
	if len(args) != 1 || len(args[0]) > 0 && args[0][0] == '-' {
		labDistillUsage()
		return 2
	}
	result, err := lab.Distill(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}
	fmt.Printf("evidence\t%s\n", result.EvidenceDir)
	for _, task := range result.Tasks {
		status := "pass"
		if !task.Passed {
			status = "fail"
		}
		if len(task.FailureKinds) > 0 {
			status += " (" + joinFailureKinds(task.FailureKinds) + ")"
		}
		fmt.Printf("%s\t%s\n", task.TaskID, status)
	}
	return 0
}

func joinFailureKinds(kinds []lab.FailureKind) string {
	values := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		values = append(values, string(kind))
	}
	return strings.Join(values, ", ")
}

func labProposalCommand(args []string) int {
	if len(args) == 0 {
		labProposalUsage()
		return 2
	}
	switch args[0] {
	case "create":
		cfg, ok := parseLabProposalCreateArgs(args[1:])
		if !ok {
			labProposalUsage()
			return 2
		}
		created, err := lab.CreateProposal(lab.ProposalCreateOptions{
			BaseSnapshot: cfg.base,
			Name:         cfg.name,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Printf("proposal\t%s\n", created.Dir)
		return 0
	case "check":
		if len(args) != 2 || len(args[1]) > 0 && args[1][0] == '-' {
			labProposalUsage()
			return 2
		}
		check, err := lab.CheckProposal(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		if len(check.Errors) > 0 {
			for _, msg := range check.Errors {
				fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, msg)
			}
			return 1
		}
		fmt.Printf("ok\t%s\n", check.Manifest.ProposalID)
		return 0
	case "status":
		if len(args) != 2 || len(args[1]) > 0 && args[1][0] == '-' {
			labProposalUsage()
			return 2
		}
		status, err := lab.ReadProposalStatus(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Printf("status\t%s\t%s\n", status.Status.ProposalID, status.Status.State)
		return 0
	case "apply":
		cfg, ok := parseLabProposalApplyArgs(args[1:])
		if !ok {
			labProposalUsage()
			return 2
		}
		mode, err := trace.ParseMode(cfg.traceMode)
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			labProposalUsage()
			return 2
		}
		applied, err := lab.ApplyProposal(context.Background(), lab.ProposalApplyOptions{
			Dir: cfg.dir, EvalPath: cfg.evalPath, Bin: cfg.bin,
			Model: cfg.model, TraceMode: mode,
		})
		if applied.ProposalID != "" {
			fmt.Printf("proposal\t%s\n", applied.ProposalID)
			if applied.TargetSnapshot != "" {
				fmt.Printf("target_snapshot\t%s\n", applied.TargetSnapshot)
			}
			if applied.ResultPath != "" {
				fmt.Printf("apply_result\t%s\n", applied.ResultPath)
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		return 0
	case "accept":
		cfg, ok := parseLabProposalAcceptArgs(args[1:])
		if !ok {
			labProposalUsage()
			return 2
		}
		accepted, err := lab.AcceptProposal(lab.ProposalAcceptOptions{
			Dir: cfg.dir, Activate: cfg.activate, PinTarget: cfg.pinTarget,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Printf("accepted\t%s\n", accepted.Status.ProposalID)
		return 0
	case "reject":
		cfg, ok := parseLabProposalRejectArgs(args[1:])
		if !ok {
			labProposalUsage()
			return 2
		}
		rejected, err := lab.RejectProposal(lab.ProposalRejectOptions{Dir: cfg.dir, Reason: cfg.reason})
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Printf("rejected\t%s\n", rejected.Status.ProposalID)
		return 0
	default:
		labProposalUsage()
		return 2
	}
}

type labProposalCreateArgs struct {
	base string
	name string
}

func parseLabProposalCreateArgs(args []string) (labProposalCreateArgs, bool) {
	var cfg labProposalCreateArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--base":
			i++
			if i >= len(args) {
				return labProposalCreateArgs{}, false
			}
			cfg.base = strings.TrimSpace(args[i])
		case "--name":
			i++
			if i >= len(args) {
				return labProposalCreateArgs{}, false
			}
			cfg.name = strings.TrimSpace(args[i])
		default:
			return labProposalCreateArgs{}, false
		}
	}
	return cfg, cfg.base != "" && cfg.name != ""
}

type labProposalAcceptArgs struct {
	dir       string
	activate  bool
	pinTarget bool
}

func parseLabProposalAcceptArgs(args []string) (labProposalAcceptArgs, bool) {
	var cfg labProposalAcceptArgs
	for _, arg := range args {
		switch arg {
		case "--activate":
			cfg.activate = true
		case "--pin-target":
			cfg.pinTarget = true
		default:
			if len(arg) > 0 && arg[0] == '-' {
				return labProposalAcceptArgs{}, false
			}
			if cfg.dir != "" {
				return labProposalAcceptArgs{}, false
			}
			cfg.dir = arg
		}
	}
	return cfg, cfg.dir != ""
}

type labProposalApplyArgs struct {
	dir       string
	evalPath  string
	bin       string
	model     string
	traceMode string
}

func parseLabProposalApplyArgs(args []string) (labProposalApplyArgs, bool) {
	cfg := labProposalApplyArgs{bin: lab.DefaultReasonixBin(), traceMode: string(trace.ModeMetadata)}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--eval":
			i++
			if i >= len(args) {
				return labProposalApplyArgs{}, false
			}
			cfg.evalPath = strings.TrimSpace(args[i])
		case "--bin":
			i++
			if i >= len(args) {
				return labProposalApplyArgs{}, false
			}
			cfg.bin = args[i]
		case "--model":
			i++
			if i >= len(args) {
				return labProposalApplyArgs{}, false
			}
			cfg.model = args[i]
		case "--trace-mode":
			i++
			if i >= len(args) {
				return labProposalApplyArgs{}, false
			}
			cfg.traceMode = args[i]
		default:
			if len(args[i]) > 0 && args[i][0] == '-' {
				return labProposalApplyArgs{}, false
			}
			if cfg.dir != "" {
				return labProposalApplyArgs{}, false
			}
			cfg.dir = args[i]
		}
	}
	return cfg, cfg.dir != ""
}

type labProposalRejectArgs struct {
	dir    string
	reason string
}

func parseLabProposalRejectArgs(args []string) (labProposalRejectArgs, bool) {
	var cfg labProposalRejectArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--reason":
			i++
			if i >= len(args) {
				return labProposalRejectArgs{}, false
			}
			cfg.reason = strings.TrimSpace(args[i])
		default:
			if len(args[i]) > 0 && args[i][0] == '-' {
				return labProposalRejectArgs{}, false
			}
			if cfg.dir != "" {
				return labProposalRejectArgs{}, false
			}
			cfg.dir = args[i]
		}
	}
	return cfg, cfg.dir != "" && cfg.reason != ""
}

func labCacheReportCommand(args []string) int {
	cfg, ok := parseLabCacheReportArgs(args)
	if !ok {
		labCacheReportUsage()
		return 2
	}
	report, err := lab.ReportTrace(cfg.path)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}
	if cfg.json {
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Println(string(b))
	} else {
		fmt.Print(lab.FormatCacheReport(report))
	}
	failures := lab.EvaluateCacheGate(report, lab.CacheGateOptions{
		Enabled:               cfg.gate,
		MinHitRatio:           cfg.minHitRatio,
		MaxContractViolations: cfg.maxContractViolations,
	})
	if len(failures) > 0 {
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, "cache gate:", failure)
		}
		return 1
	}
	return 0
}

type labCacheReportArgs struct {
	path                  string
	json                  bool
	gate                  bool
	minHitRatio           float64
	maxContractViolations int
}

func parseLabCacheReportArgs(args []string) (labCacheReportArgs, bool) {
	cfg := labCacheReportArgs{maxContractViolations: 0}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			cfg.json = true
		case "--gate":
			cfg.gate = true
		case "--min-hit-ratio":
			i++
			if i >= len(args) {
				return labCacheReportArgs{}, false
			}
			v, err := strconv.ParseFloat(args[i], 64)
			if err != nil || v < 0 || v > 1 {
				return labCacheReportArgs{}, false
			}
			cfg.minHitRatio = v
		case "--max-contract-violations":
			i++
			if i >= len(args) {
				return labCacheReportArgs{}, false
			}
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 0 {
				return labCacheReportArgs{}, false
			}
			cfg.maxContractViolations = v
		default:
			if len(arg) > 0 && arg[0] == '-' {
				return labCacheReportArgs{}, false
			}
			if cfg.path != "" {
				return labCacheReportArgs{}, false
			}
			cfg.path = arg
		}
	}
	return cfg, cfg.path != ""
}

func labEvalCommand(args []string) int {
	cfg, ok := parseLabEvalArgs(args)
	if !ok {
		labEvalUsage()
		return 2
	}
	mode, err := trace.ParseMode(cfg.traceMode)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		labEvalUsage()
		return 2
	}
	result, err := (lab.Runner{Options: lab.Options{
		Bin:       cfg.bin,
		Model:     cfg.model,
		TraceMode: mode,
	}}).Run(context.Background(), cfg.path)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
		return 1
	}
	for _, task := range result.Tasks {
		status := "fail"
		if task.Passed {
			status = "pass"
		}
		if task.CacheWarning {
			status += " (cache warning)"
		}
		fmt.Printf("%s\t%s\n", task.TaskID, status)
	}
	fmt.Printf("result\t%s\n", result.ArtifactDir)
	if !result.Passed {
		return 1
	}
	return 0
}

type labEvalArgs struct {
	path      string
	bin       string
	model     string
	traceMode string
}

func parseLabEvalArgs(args []string) (labEvalArgs, bool) {
	cfg := labEvalArgs{bin: lab.DefaultReasonixBin(), traceMode: string(trace.ModeMetadata)}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--bin":
			i++
			if i >= len(args) {
				return labEvalArgs{}, false
			}
			cfg.bin = args[i]
		case "--model":
			i++
			if i >= len(args) {
				return labEvalArgs{}, false
			}
			cfg.model = args[i]
		case "--trace-mode":
			i++
			if i >= len(args) {
				return labEvalArgs{}, false
			}
			cfg.traceMode = args[i]
		default:
			if len(arg) > 0 && arg[0] == '-' {
				return labEvalArgs{}, false
			}
			if cfg.path != "" {
				return labEvalArgs{}, false
			}
			cfg.path = arg
		}
	}
	return cfg, cfg.path != ""
}

func labHarnessCommand(args []string) int {
	if len(args) == 0 {
		labHarnessUsage()
		return 2
	}
	layout := harness.DefaultLayout()
	switch args[0] {
	case "init":
		if len(args) != 1 {
			labHarnessUsage()
			return 2
		}
		if err := layout.Init(); err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Printf("initialized %s\n", harness.RootDir)
		return 0
	case "snapshot":
		return labHarnessSnapshotCommand(layout, args[1:])
	case "inspect":
		if len(args) != 2 {
			labHarnessUsage()
			return 2
		}
		lock, err := layout.Inspect(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		b, err := json.MarshalIndent(lock, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Println(string(b))
		return 0
	default:
		labHarnessUsage()
		return 2
	}
}

func labHarnessSnapshotCommand(layout harness.Layout, args []string) int {
	if len(args) == 0 {
		labHarnessUsage()
		return 2
	}
	switch args[0] {
	case "create":
		if len(args) != 1 {
			labHarnessUsage()
			return 2
		}
		lock, err := layout.CreateSnapshot(time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Printf("created %s\n", lock.SnapshotID)
		return 0
	case "list":
		if len(args) != 1 {
			labHarnessUsage()
			return 2
		}
		locks, err := layout.ListSnapshots()
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		pinned, err := layout.Pinned()
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		pinnedSet := map[string]bool{}
		for _, id := range pinned {
			pinnedSet[id] = true
		}
		for _, lock := range locks {
			marker := ""
			if pinnedSet[lock.SnapshotID] {
				marker = "\tpinned"
			}
			fmt.Printf("%s\t%s\t%s%s\n", lock.SnapshotID, lock.CreatedAt.Format(time.RFC3339), lock.StablePrefixHash, marker)
		}
		return 0
	case "activate":
		if len(args) != 2 {
			labHarnessUsage()
			return 2
		}
		if err := layout.Activate(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Printf("activated %s\n", args[1])
		return 0
	case "pin":
		if len(args) != 2 {
			labHarnessUsage()
			return 2
		}
		if err := layout.Pin(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Printf("pinned %s\n", args[1])
		return 0
	case "unpin":
		if len(args) != 2 {
			labHarnessUsage()
			return 2
		}
		if err := layout.Unpin(args[1]); err != nil {
			fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
			return 1
		}
		fmt.Printf("unpinned %s\n", args[1])
		return 0
	default:
		labHarnessUsage()
		return 2
	}
}

func labUsage() {
	fmt.Fprintln(os.Stderr, "Usage: reasonix lab <harness|eval|cache-report|distill|proposal|gc>")
}

func labHarnessUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  reasonix lab harness init")
	fmt.Fprintln(os.Stderr, "  reasonix lab harness snapshot create")
	fmt.Fprintln(os.Stderr, "  reasonix lab harness snapshot list")
	fmt.Fprintln(os.Stderr, "  reasonix lab harness snapshot activate <snapshot-id>")
	fmt.Fprintln(os.Stderr, "  reasonix lab harness snapshot pin <snapshot-id>")
	fmt.Fprintln(os.Stderr, "  reasonix lab harness snapshot unpin <snapshot-id>")
	fmt.Fprintln(os.Stderr, "  reasonix lab harness inspect <snapshot-id>")
}

func labEvalUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  reasonix lab eval <task-or-suite> [--bin path] [--model name] [--trace-mode metadata|preview|full]")
}

func labCacheReportUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  reasonix lab cache-report <trace.jsonl> [--json] [--gate] [--min-hit-ratio FLOAT] [--max-contract-violations N]")
}

func labDistillUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  reasonix lab distill <eval-run-dir>")
}

func labProposalUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  reasonix lab proposal create --base <snapshot-id> --name <name>")
	fmt.Fprintln(os.Stderr, "  reasonix lab proposal check <proposal-dir>")
	fmt.Fprintln(os.Stderr, "  reasonix lab proposal status <proposal-dir>")
	fmt.Fprintln(os.Stderr, "  reasonix lab proposal apply <proposal-dir> [--eval <task-or-suite>] [--bin path] [--model name] [--trace-mode metadata|preview|full]")
	fmt.Fprintln(os.Stderr, "  reasonix lab proposal accept <proposal-dir> [--activate] [--pin-target]")
	fmt.Fprintln(os.Stderr, "  reasonix lab proposal reject <proposal-dir> --reason <text>")
}

func labGCUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  reasonix lab gc --dry-run")
}
