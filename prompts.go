package main

import "fmt"

// generateUpPrompt returns the initial user message for the `up` subcommand.
// The detailed step-by-step procedure lives in the turf MCP server as the
// `/up` prompt; this wrapper triggers that flow and points the agent at the
// codified-IaC skill for any concepts it needs along the way. The turf MCP
// tools are exposed to the model with a `turf_` prefix (see agent.go), so the
// skill tools are named with that prefix here.
func generateUpPrompt(configPath string) string {
	return fmt.Sprintf(
		"Deploy the infrastructure declared at %q by running the turf MCP server's `/up` prompt against that path. "+
			"Load `turf_skill_codified` if you need conceptual guidance on reconciliation, lifecycle options, or replan hints, "+
			"and `turf_skill_core` for the underlying plan/apply discipline.",
		configPath,
	)
}

// generateDestroyPrompt returns the initial user message for the `destroy`
// subcommand. It delegates to the turf MCP server's `/destroy` prompt. As in
// generateUpPrompt, skill tools carry the `turf_` prefix (see agent.go).
func generateDestroyPrompt(configPath string) string {
	return fmt.Sprintf(
		"Tear down the infrastructure declared at %q by running the turf MCP server's `/destroy` prompt against that path. "+
			"Load `turf_skill_codified` if you need conceptual guidance on destruction ordering or lifecycle constraints.",
		configPath,
	)
}
