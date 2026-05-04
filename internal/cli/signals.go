package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	isatty "github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/mainline-org/mainline/internal/domain"
	"github.com/mainline-org/mainline/internal/engine"
)

var guardCmd = &cobra.Command{
	Use:   "guard",
	Short: "Manage human-confirmed constraints",
	Long: `Manage constraints that future agents must see and obey.

Constraints are high-authority action signals. They are not created by
seal output; add them only when a human explicitly confirms the rule.`,
}

var riskCmd = &cobra.Command{
	Use:   "risk",
	Short: "Create explicit review-facing risks",
	Long: `Create explicit risks with a concrete failure mode.

Use "mainline risks" to list and resolve existing risks.`,
}

var followupCmd = &cobra.Command{
	Use:   "followup",
	Short: "Create explicit deferred work items",
	Long: `Create explicit follow-ups with provenance.

Use "mainline followups" to list and resolve existing follow-ups.`,
}

var (
	guardAddIntent     string
	guardAddFiles      []string
	guardAddWhy        string
	guardAddSeverity   string
	guardAddSourceNote string
)

var guardAddCmd = &cobra.Command{
	Use:   "add <constraint>",
	Short: "Add a human-confirmed constraint",
	Long: `Add a constraint future agents must see before editing matching files.

This command is intentionally interactive. Non-interactive callers cannot
create constraints because constraints are human-granted rules, not agent
seal prose.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if !isatty.IsTerminal(os.Stdin.Fd()) {
			outputError(domain.NewRecoverableError(domain.ErrInvalidInput,
				"guard add requires interactive human confirmation",
				"run this command in a terminal and type the confirmation phrase",
				"use review_notes for agent observations that are not human-promoted constraints",
			))
			return
		}
		fmt.Println("Add Mainline constraint?")
		fmt.Printf("  constraint: %s\n", args[0])
		fmt.Printf("  why:        %s\n", guardAddWhy)
		if len(guardAddFiles) > 0 {
			fmt.Printf("  files:      %s\n", strings.Join(guardAddFiles, ", "))
		}
		if guardAddIntent != "" {
			fmt.Printf("  intent:     %s\n", guardAddIntent)
		}
		fmt.Print("Type \"add constraint\" to confirm: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != "add constraint" {
			outputError(domain.NewRecoverableError(domain.ErrInvalidInput,
				"constraint add cancelled",
				"no constraint was written",
			))
			return
		}
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		constraint, err := svc.AddConstraint(engine.AddConstraintInput{
			IntentID:   guardAddIntent,
			Files:      guardAddFiles,
			What:       args[0],
			Why:        guardAddWhy,
			Severity:   guardAddSeverity,
			Source:     domain.SignalSourceExplicitUser,
			SourceNote: guardAddSourceNote,
		})
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(constraint)
			return
		}
		fmt.Printf("Constraint added: %s\n", constraint.ID)
	},
}

var (
	riskAddIntent     string
	riskAddFiles      []string
	riskAddTrigger    string
	riskAddImpact     string
	riskAddMitigation string
	riskAddValidation string
	riskAddOwner      string
)

var riskAddCmd = &cobra.Command{
	Use:   "add <failure_mode>",
	Short: "Add an explicit structured risk",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		risk, err := svc.AddRisk(engine.AddRiskInput{
			IntentID: riskAddIntent,
			Files:    riskAddFiles,
			Statement: domain.RiskStatement{
				FailureMode: args[0],
				Trigger:     riskAddTrigger,
				Impact:      riskAddImpact,
				Mitigation:  riskAddMitigation,
				Validation:  riskAddValidation,
				Owner:       riskAddOwner,
			},
		})
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(risk)
			return
		}
		fmt.Printf("Risk added: %s\n", risk.ID)
	},
}

var (
	followupAddIntent     string
	followupAddFiles      []string
	followupAddSource     string
	followupAddSourceNote string
	followupAddReference  string
	followupAddOwner      string
	followupAddDue        string
)

var followupAddCmd = &cobra.Command{
	Use:   "add <task>",
	Short: "Add an explicit follow-up",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		svc, err := getService()
		if err != nil {
			outputError(err)
			return
		}
		followup, err := svc.AddFollowup(engine.AddFollowupInput{
			IntentID: followupAddIntent,
			Files:    followupAddFiles,
			Statement: domain.FollowupStatement{
				Task:       args[0],
				Source:     followupAddSource,
				SourceNote: followupAddSourceNote,
				Reference:  followupAddReference,
				Owner:      followupAddOwner,
				Due:        followupAddDue,
			},
		})
		if err != nil {
			outputError(err)
			return
		}
		if jsonOutput {
			outputJSON(followup)
			return
		}
		fmt.Printf("Follow-up added: %s\n", followup.ID)
	},
}

func init() {
	guardAddCmd.Flags().StringVar(&guardAddIntent, "intent", "", "source intent id")
	guardAddCmd.Flags().StringArrayVar(&guardAddFiles, "file", nil, "file path the constraint applies to")
	guardAddCmd.Flags().StringVar(&guardAddWhy, "why", "", "load-bearing reason future agents must respect")
	guardAddCmd.Flags().StringVar(&guardAddSeverity, "severity", "high", "severity: high, medium, low")
	guardAddCmd.Flags().StringVar(&guardAddSourceNote, "source-note", "", "human approval note or reviewer context")
	guardAddCmd.MarkFlagRequired("why")
	guardCmd.AddCommand(guardAddCmd)

	riskAddCmd.Flags().StringVar(&riskAddIntent, "intent", "", "source intent id (defaults to active draft)")
	riskAddCmd.Flags().StringArrayVar(&riskAddFiles, "file", nil, "file path the risk applies to")
	riskAddCmd.Flags().StringVar(&riskAddTrigger, "trigger", "", "trigger condition")
	riskAddCmd.Flags().StringVar(&riskAddImpact, "impact", "", "impact surface")
	riskAddCmd.Flags().StringVar(&riskAddMitigation, "mitigation", "", "mitigation")
	riskAddCmd.Flags().StringVar(&riskAddValidation, "validation", "", "validation evidence")
	riskAddCmd.Flags().StringVar(&riskAddOwner, "owner", "", "owner")
	riskCmd.AddCommand(riskAddCmd)

	followupAddCmd.Flags().StringVar(&followupAddIntent, "intent", "", "source intent id (defaults to active draft)")
	followupAddCmd.Flags().StringArrayVar(&followupAddFiles, "file", nil, "file path the follow-up applies to")
	followupAddCmd.Flags().StringVar(&followupAddSource, "source", "", "source: explicit_defer, external_reference, cut_scope")
	followupAddCmd.Flags().StringVar(&followupAddSourceNote, "source-note", "", "required for explicit_defer or cut_scope")
	followupAddCmd.Flags().StringVar(&followupAddReference, "reference", "", "required for external_reference")
	followupAddCmd.Flags().StringVar(&followupAddOwner, "owner", "", "owner")
	followupAddCmd.Flags().StringVar(&followupAddDue, "due", "", "due date")
	followupCmd.AddCommand(followupAddCmd)
}
