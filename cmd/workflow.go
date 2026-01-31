package cmd

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/temporal"
	"github.com/spf13/cobra"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
)

func cmdWorkflow() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workflow",
		Aliases: []string{"wf"},
		Short:   "Workflow operations",
		Long:    `Workflow management commands: download, cancel.`,
	}
	cmd.AddCommand(cmdDownload(), cmdWorkflowCancel())
	return cmd
}

func cmdWorkflowCancel() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel open workflows",
		Long:  `Cancel all open Temporal workflows, falling back to terminate if needed`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return config.BindTemporalFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cancelAllWorkflows(cmd.Context())
		},
	}
	config.InitTemporalFlags(cmd.Flags())
	return cmd
}

func cancelAllWorkflows(ctx context.Context) error {
	client, err := temporal.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create temporal client: %w", err)
	}
	defer client.Close()

	// List open workflows
	var nextPageToken []byte
	totalCancelled := 0
	totalFailed := 0

	for {
		resp, err := client.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Namespace:     "default",
			PageSize:      1000,
			NextPageToken: nextPageToken,
			Query:         fmt.Sprintf("ExecutionStatus = %d", enums.WORKFLOW_EXECUTION_STATUS_RUNNING),
		})
		if err != nil {
			return fmt.Errorf("failed to list workflows: %w", err)
		}

		if len(resp.Executions) == 0 && nextPageToken == nil {
			log.Info().Msg("No open workflows found")
			return nil
		}

		log.Info().Int("count", len(resp.Executions)).Msg("Found open workflow(s)")

		for _, execution := range resp.Executions {
			workflowID := execution.Execution.WorkflowId
			runID := execution.Execution.RunId

			log.Info().Str("workflowId", workflowID).Msg("Cancelling workflow")

			// Try cancel first (doesn't require admin)
			err := client.CancelWorkflow(ctx, workflowID, runID)
			if err != nil {
				log.Warn().Err(err).Str("workflowId", workflowID).Msg("Cancel failed, trying terminate")

				// Fall back to terminate
				err = client.TerminateWorkflow(ctx, workflowID, runID, "terminated via CLI")
				if err != nil {
					log.Error().Err(err).Str("workflowId", workflowID).Msg("Terminate also failed")
					totalFailed++
					continue
				}
			}
			totalCancelled++
		}

		nextPageToken = resp.NextPageToken
		if len(nextPageToken) == 0 {
			break
		}
	}

	log.Info().Int("cancelled", totalCancelled).Int("failed", totalFailed).Msg("Workflow cancellation complete")
	return nil
}
