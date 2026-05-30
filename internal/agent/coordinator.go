package agent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"reasonix/internal/provider"
)

// Runner carries out one task turn. Both Agent (single model) and Coordinator
// (two-model) satisfy it, so the CLI stays agnostic to which is in use.
type Runner interface {
	Run(ctx context.Context, input string) error
}

// DefaultPlannerPrompt steers the planner toward concise plans, not execution.
const DefaultPlannerPrompt = `You are the planner in a two-model coding agent.
Given a task, produce a concise, ordered plan for the executor model to carry out.
Do not write full implementations or call tools — outline the steps, which files
to touch, and the key decisions. Keep it short and actionable.`

// Coordinator runs two models in separate sessions to keep each one's prompt
// prefix cache-stable: a low-frequency planner proposes an approach, then the
// executor (a full tool-using Agent) carries it out. The sessions never mix, so
// neither model's prefix is disturbed by the other's turns.
type Coordinator struct {
	planner        provider.Provider
	plannerSess    *Session
	plannerPricing *provider.Pricing
	executor       *Agent
	temperature    float64
	out            io.Writer
}

// NewCoordinator wires a planner provider (with its own session) to an executor.
func NewCoordinator(planner provider.Provider, plannerSession *Session, plannerPricing *provider.Pricing, executor *Agent, temperature float64, out io.Writer) *Coordinator {
	return &Coordinator{
		planner:        planner,
		plannerSess:    plannerSession,
		plannerPricing: plannerPricing,
		executor:       executor,
		temperature:    temperature,
		out:            out,
	}
}

// Run plans with the planner model, then hands the plan to the executor.
func (c *Coordinator) Run(ctx context.Context, input string) error {
	fmt.Fprintf(c.out, "[%s · planning]\n", c.planner.Name())
	plan, err := c.plan(ctx, input)
	if err != nil {
		return fmt.Errorf("planner: %w", err)
	}
	fmt.Fprintf(c.out, "\n[%s · executing]\n", c.executor.prov.Name())
	return c.executor.Run(ctx, formatHandoff(input, plan))
}

// plan streams a plan from the planner (no tools) and appends it to the planner
// session, so that session grows prepend-only and stays cache-friendly.
func (c *Coordinator) plan(ctx context.Context, input string) (string, error) {
	c.plannerSess.Add(provider.Message{Role: provider.RoleUser, Content: input})

	ch, err := c.planner.Stream(ctx, provider.Request{
		Messages:    c.plannerSess.Messages,
		Temperature: c.temperature,
	})
	if err != nil {
		return "", err
	}

	var text strings.Builder
	var usage *provider.Usage
	for chunk := range ch {
		switch chunk.Type {
		case provider.ChunkText:
			text.WriteString(chunk.Text)
			fmt.Fprint(c.out, chunk.Text)
		case provider.ChunkUsage:
			usage = chunk.Usage
		case provider.ChunkError:
			return "", chunk.Err
		}
	}
	if text.Len() > 0 {
		fmt.Fprintln(c.out)
	}
	printUsage(c.out, usage, c.plannerPricing)

	plan := text.String()
	c.plannerSess.Add(provider.Message{Role: provider.RoleAssistant, Content: plan})
	return plan, nil
}

func formatHandoff(task, plan string) string {
	return fmt.Sprintf("Task: %s\n\nA planner proposed this approach:\n%s\n\nCarry it out, adapting as needed.", task, plan)
}
