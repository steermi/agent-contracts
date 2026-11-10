package activity_test

import (
	"fmt"

	"github.com/steermi/agent-contracts/activity"
)

// A Build run moves from suggestion to submission. Submitting evidence is not
// acceptance: a different person accepts the exact evidence digest, and only
// after provider-verified checks pass.
func Example() {
	dev := activity.Actor{OrgID: "org", UserID: "dev", Role: "member"}
	reviewer := activity.Actor{OrgID: "org", UserID: "reviewer", Role: "member"}
	ci := activity.Actor{OrgID: "org", UserID: "git", Purpose: activity.PurposeVerifiedGit}

	run, _ := activity.NewRun("run-1", "org", "project", "item", dev, activity.Request{
		Runner: activity.Code, Location: activity.Local, InputDigest: activity.Digest("ticket@1"),
		Policy: activity.Policy{RequiredChecks: []string{"ci"}},
		// The delivery flow pins what follows: a done Build goes to Review.
		Flow: &activity.FlowRef{VersionID: "v1", Node: "build", Hops: map[activity.Outcome]activity.FlowHop{
			activity.OutcomeDone: {Node: "review", Runner: activity.Findings},
		}},
	}, nil)
	apply := func(a activity.Actor, c activity.Command) {
		c.ExpectedVersion = run.Version
		fmt.Printf("%-9s by %-8s → %v (state %s)\n", c.Type, a.UserID, run.Apply(a, c), run.State)
	}

	commit := activity.OutputRef{Kind: activity.RefRepository, ResourceID: "mr-1", RepositoryID: "repo", CommitSHA: "0123456789abcdef0123456789abcdef01234567"}
	apply(dev, activity.Command{Type: activity.Confirm})
	apply(dev, activity.Command{Type: activity.Start})
	apply(dev, activity.Command{Type: activity.Submit, Outputs: []activity.OutputRef{commit}})
	apply(dev, activity.Command{Type: activity.Decide, Decision: activity.Accept, EvidenceDigest: run.EvidenceDigest()})
	apply(reviewer, activity.Command{Type: activity.Decide, Decision: activity.Accept, EvidenceDigest: run.EvidenceDigest()})
	apply(ci, activity.Command{Type: activity.RecordCheck, Check: &activity.Check{
		RepositoryID: "repo", CommitSHA: commit.CommitSHA, Name: "ci", ProviderRunID: "1", Attempt: 1, Status: activity.CheckSuccess,
	}})
	apply(reviewer, activity.Command{Type: activity.Decide, Decision: activity.Accept, EvidenceDigest: run.EvidenceDigest()})
	fmt.Println("accepted:", run.HumanAccepted(), "next:", run.NextRunner())

	// Output:
	// confirm   by dev      → <nil> (state ready)
	// start     by dev      → <nil> (state running)
	// submit    by dev      → <nil> (state submitted)
	// decision  by dev      → ACTIVITY_FORBIDDEN (state submitted)
	// decision  by reviewer → ACTIVITY_CHECKS_PENDING (state submitted)
	// check     by git      → <nil> (state submitted)
	// decision  by reviewer → <nil> (state submitted)
	// accepted: true next: findings
}

// Capabilities are allowlists: Review can read and comment, never write code.
func ExampleCapabilitiesFor() {
	review := activity.CapabilitiesFor(activity.Findings, "project")
	build := activity.CapabilitiesFor(activity.Code, "project")
	fmt.Println(review.Allows("read_file"), review.Allows("write_file"), build.Allows("write_file"))
	// Output: true false true
}
