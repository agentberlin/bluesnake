package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/spf13/cobra"
)

// `bluesnake queue` inspects and manages the persistent crawl queue (the
// registry `jobs` table). Jobs are run by a long-lived host — the desktop app —
// so from the CLI this lists what is queued/running/done and lets you cancel a
// queued job; a one-shot `bluesnake crawl` runs in its own process and does not
// touch this queue.
func newQueueCmd() *cobra.Command {
	var storeDir string
	queueCmd := &cobra.Command{
		Use:   "queue",
		Short: "Inspect and manage the persistent crawl queue",
	}

	lsCmd := &cobra.Command{
		Use:   "ls",
		Short: "List queued, running and recent crawl jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			jobs, err := store.ListJobs(storeDir)
			if err != nil {
				return err
			}
			if len(jobs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "queue is empty")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "JOB\tSTATUS\tSOURCE\tCRAWL\tLABEL")
			for _, j := range jobs {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", j.ID, j.Status, j.Source, j.CrawlID, j.Label)
			}
			return w.Flush()
		},
	}
	lsCmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")

	rmCmd := &cobra.Command{
		Use:   "rm <job-id>",
		Short: "Cancel a queued job, or remove a finished one from the list",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobs, err := store.ListJobs(storeDir)
			if err != nil {
				return err
			}
			var found *store.Job
			for i := range jobs {
				if jobs[i].ID == args[0] {
					found = &jobs[i]
				}
			}
			if found == nil {
				return fmt.Errorf("job %s not found", args[0])
			}
			switch found.Status {
			case store.JobQueued:
				if _, err := store.CancelJob(storeDir, args[0]); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "canceled queued job %s\n", args[0])
			case store.JobRunning:
				return fmt.Errorf("job %s is running; stop it from the app", args[0])
			default:
				if err := store.DeleteJob(storeDir, args[0]); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "removed job %s\n", args[0])
			}
			return nil
		},
	}
	rmCmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")

	queueCmd.AddCommand(lsCmd, rmCmd)
	return queueCmd
}
