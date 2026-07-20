// Package report renders human-readable and machine-readable output.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/restayway/regbot/pkg/plan"
)

func JSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func PlanTable(writer io.Writer, proposal plan.Plan) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	fmt.Fprintf(table, "ACTION\tREGISTRY\tREPOSITORY\tTAGS\tDIGEST/ID\tREASON\n")
	for _, action := range proposal.Actions {
		fmt.Fprintf(table, "DELETE\t%s\t%s\t%v\t%s\t%v\n", action.Artifact.Registry, action.Artifact.Repository, action.Artifact.Tags, action.Artifact.ID, action.ReasonCodes)
	}
	fmt.Fprintf(table, "\nDiscovered: %d\tProtected: %d\tPlanned deletions: %d\tPlan ID: %s\n", proposal.DiscoveredCount, proposal.ProtectedCount, len(proposal.Actions), proposal.ID())
	return table.Flush()
}

func ResultTable(writer io.Writer, result plan.Result) error {
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	fmt.Fprintf(table, "STATUS\tREGISTRY\tREPOSITORY\tID\tERROR\n")
	for _, outcome := range result.Outcomes {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", outcome.Status, outcome.Action.Artifact.Registry, outcome.Action.Artifact.Repository, outcome.Action.Artifact.ID, outcome.Error)
	}
	fmt.Fprintf(table, "\nPlanned: %d\tDeleted: %d\tSkipped: %d\tFailed: %d\n", result.Planned, result.Deleted, result.Skipped, result.Failed)
	return table.Flush()
}
