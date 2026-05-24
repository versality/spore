package testagent

import (
	"os"
	"strconv"
)

func recordCoordinatorContract(rec recorder, provider, mode string) {
	env := selectedEnv()
	if env["WT_SESSION_KIND"] != "coordinator" && env["SPORE_TASK_SLUG"] != "coordinator" {
		return
	}
	fields := map[string]string{
		"slug":      env["SPORE_TASK_SLUG"],
		"role_file": env["SPORE_COORDINATOR_ROLE"],
	}
	if env["SPORE_TASK_SLUG"] != "coordinator" {
		_ = rec.event(Event{Type: "coordinator-contract-error", Provider: provider, Mode: mode, Error: "SPORE_TASK_SLUG must be coordinator", Fields: fields})
	}
	rolePath := env["SPORE_COORDINATOR_ROLE"]
	if rolePath == "" {
		_ = rec.event(Event{Type: "coordinator-contract-warning", Provider: provider, Mode: mode, Error: "SPORE_COORDINATOR_ROLE is not set", Fields: fields})
		return
	}
	body, err := os.ReadFile(rolePath)
	if err != nil {
		_ = rec.event(Event{Type: "coordinator-contract-warning", Provider: provider, Mode: mode, Error: err.Error(), Fields: fields})
		return
	}
	fields["role_bytes"] = strconv.Itoa(len(body))
	_ = rec.event(Event{Type: "coordinator-contract", Provider: provider, Mode: mode, Fields: fields})
}
