package cloudformation

// diagnostics_ecs.go — the ECS collector: why an AWS::ECS::Service failed to
// stabilize, assembled from what ECS still knows at the moment CloudFormation
// gives up on it.
//
// Everything here is gathered over the emulator router, the same way
// provisioner_ecs.go creates and deletes the service. There is no Go import of
// internal/services/ecs and there must not be one — see diagnosticCollector.
//
// PRIVACY. This collector never reads a task definition. That is not an
// oversight: `environment` and `secrets` live there, and the payload may not
// carry an environment-variable value, a secret, or a resolved parameter under
// any circumstances. What a container printed about itself is different and is
// left exactly as printed — that text is the diagnosis, and scrubbing it would
// remove the answer. The rule is that Overcast never *introduces* a value the
// container did not print.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const ecsDiagnosticsTarget = "AmazonEC2ContainerServiceV20141113."

// ── The projections this collector reads ───────────────────────────────────
//
// Deliberately narrow. A wide struct would decode fields nobody renders and
// invite one of them into the payload later; these carry exactly what the
// panes below are built from.

type diagECSService struct {
	ServiceName  string              `json:"serviceName"`
	DesiredCount int                 `json:"desiredCount"`
	RunningCount int                 `json:"runningCount"`
	Deployments  []diagECSDeployment `json:"deployments"`
	Events       []diagECSEvent      `json:"events"`
}

type diagECSDeployment struct {
	Status             string `json:"status"`
	FailedTasks        int    `json:"failedTasks"`
	RolloutState       string `json:"rolloutState"`
	RolloutStateReason string `json:"rolloutStateReason"`
}

type diagECSEvent struct {
	CreatedAt float64 `json:"createdAt"`
	Message   string  `json:"message"`
}

type diagECSTask struct {
	TaskArn       string           `json:"taskArn"`
	Group         string           `json:"group"`
	StoppedReason string           `json:"stoppedReason"`
	StopCode      string           `json:"stopCode"`
	StartedAt     *float64         `json:"startedAt"`
	StoppedAt     *float64         `json:"stoppedAt"`
	Containers    []diagECSContain `json:"containers"`
}

type diagECSContain struct {
	Name     string `json:"name"`
	ExitCode *int   `json:"exitCode"`
	Reason   string `json:"reason"`
}

// collectECSServiceEvidence gathers the diagnosis for a service that could not
// keep its tasks alive.
//
// The order is the order a person would look in: what the scheduler said, what
// state the deployment reached, what the stopped tasks report, and finally what
// the containers themselves printed — which is nearly always the answer and is
// the one part that no amount of AWS-fidelity work could have surfaced.
func collectECSServiceEvidence(ctx context.Context, router http.Handler, region string, res StackResource) collectedEvidence {
	serviceARN := res.PhysicalID
	cluster := ecsClusterFromServiceARN(serviceARN)

	var out collectedEvidence
	svc, haveService := describeECSServiceForDiagnostics(ctx, router, region, cluster, serviceARN)
	if haveService {
		if section, ok := ecsServiceEventsSection(svc); ok {
			out.Sections = append(out.Sections, section)
		}
		if section, ok := ecsDeploymentSection(svc); ok {
			out.Sections = append(out.Sections, section)
		}
	}

	// The service name comes from the ARN, not from the DescribeServices
	// response. A service that could not be described leaves that response
	// zero-valued, and an empty name would turn the ownership filter below
	// into no filter at all — attributing every stopped task in the cluster,
	// including other stacks', to this one resource.
	tasks, omitted := stoppedTasksForECSService(ctx, router, region, cluster, ecsServiceNameFromARN(serviceARN))
	if section, ok := ecsStoppedTasksSection(tasks, omitted); ok {
		out.Sections = append(out.Sections, section)
	}

	logs := captureECSContainerOutput(ctx, router, region, tasks)
	out.Sections = append(out.Sections, logs...)

	out.Headline = ecsHeadline(tasks, omitted)
	out.Counterfactual = ecsCounterfactual(res.StatusReason, len(logs) > 0, len(tasks) > 0)
	return out
}

// describeECSServiceForDiagnostics reads the service one last time before
// rollback deletes it. A service that is already gone is not an error here —
// the panes it would have filled are simply absent.
func describeECSServiceForDiagnostics(ctx context.Context, router http.Handler, region, cluster, serviceARN string) (diagECSService, bool) {
	body := map[string]any{"services": []string{serviceARN}}
	if cluster != "" {
		body["cluster"] = cluster
	}
	rec, err := internalJSON(ctx, router, region, ecsDiagnosticsTarget+"DescribeServices", body)
	if err != nil {
		return diagECSService{}, false
	}
	var resp struct {
		Services []diagECSService `json:"services"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Services) == 0 {
		return diagECSService{}, false
	}
	return resp.Services[0], true
}

// stoppedTasksForECSService lists the cluster's stopped tasks and describes as
// many as the cap allows, keeping only the ones this service owns. It returns
// the described tasks and how many were left out.
//
// The service filter is applied here rather than in the request because
// Overcast's ListTasks accepts only cluster, family and desiredStatus — real
// ECS also takes serviceName, and sending a member the emulator ignores would
// look like a filter that works. Each task states its own owner in `group`
// ("service:<name>"), which is what ECS itself keys the association on.
func stoppedTasksForECSService(ctx context.Context, router http.Handler, region, cluster, serviceName string) (tasks []diagECSTask, omitted int) {
	if serviceName == "" {
		// With no name there is no way to tell this service's tasks from any
		// other's, and a wrong attribution is worse than a missing pane.
		return nil, 0
	}
	listBody := map[string]any{"desiredStatus": "STOPPED"}
	if cluster != "" {
		listBody["cluster"] = cluster
	}
	rec, err := internalJSON(ctx, router, region, ecsDiagnosticsTarget+"ListTasks", listBody)
	if err != nil {
		return nil, 0
	}
	var listed struct {
		TaskArns []string `json:"taskArns"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || len(listed.TaskArns) == 0 {
		return nil, 0
	}

	described := describeECSTasks(ctx, router, region, cluster, listed.TaskArns)
	group := "service:" + serviceName
	for _, t := range described {
		if t.Group != group {
			continue
		}
		if len(tasks) >= maxCapturedTasks {
			omitted++
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, omitted
}

// describeECSTasks fetches the stopped task records — the volatile half of the
// evidence. ECS reaps a stopped task an hour after it stops, so this is the
// part that genuinely has to be copied rather than pointed at.
func describeECSTasks(ctx context.Context, router http.Handler, region, cluster string, arns []string) []diagECSTask {
	body := map[string]any{"tasks": arns}
	if cluster != "" {
		body["cluster"] = cluster
	}
	rec, err := internalJSON(ctx, router, region, ecsDiagnosticsTarget+"DescribeTasks", body)
	if err != nil {
		return nil
	}
	var resp struct {
		Tasks []diagECSTask `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return nil
	}
	return resp.Tasks
}

// captureECSContainerOutput copies each stopped container's retained output
// into the entry, one pane per container.
//
// It is fetched through /_overcast/ecs/tasks/{task}/logs/{container} rather
// than from Docker directly, so the collector reads whatever ECS has —
// retained copy or live container — instead of reimplementing that choice. The
// task *ID* is used rather than the full ARN because an ARN contains slashes:
// percent-encoding them survives chi's routing but not the endpoint's own
// lookup, and the endpoint accepts either form.
func captureECSContainerOutput(ctx context.Context, router http.Handler, region string, tasks []diagECSTask) []DiagnosticSection {
	var sections []DiagnosticSection
	for _, t := range tasks {
		taskID := ecsTaskIDFromARN(t.TaskArn)
		for _, c := range t.Containers {
			if taskID == "" || c.Name == "" {
				continue
			}
			text, ok := fetchECSContainerLog(ctx, router, region, taskID, c.Name)
			if !ok || strings.TrimSpace(text) == "" {
				continue
			}
			trimmed := tailBytes(text, maxCapturedLogBytes)
			sections = append(sections, DiagnosticSection{
				ID:         fmt.Sprintf("ecs-container-output-%s-%s", taskID, c.Name),
				Title:      "Container output",
				Provenance: provenanceCapture,
				Note:       "Copied out of the container before rollback removed it.",
				Kind:       sectionKindLog,
				// CapturedAt is left empty: everything a collector gathers is
				// gathered in the same instant, and collectDeployDiagnostics
				// stamps it from the provisioner's clock. A collector that
				// genuinely knows a different time — one reusing an older
				// retained copy — sets it and keeps that value.
				Log: &DiagnosticLog{
					Label:     "task " + shortTaskID(taskID) + " · container " + c.Name,
					Text:      trimmed,
					Truncated: len(trimmed) < len(text),
				},
			})
		}
	}
	return sections
}

func fetchECSContainerLog(ctx context.Context, router http.Handler, region, taskID, container string) (string, bool) {
	path := "/_overcast/ecs/tasks/" + taskID + "/logs/" + container
	rec, err := internalGET(ctx, router, "ecs", region, path)
	if err != nil {
		return "", false
	}
	var resp struct {
		Logs string `json:"logs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", false
	}
	return resp.Logs, true
}

// ── Section builders ───────────────────────────────────────────────────────

// ecsServiceEventsSection is the scheduler's own history, newest first as ECS
// orders it. Provenance aws-api: `aws ecs describe-services` returns exactly
// this, for as long as the service exists.
func ecsServiceEventsSection(svc diagECSService) (DiagnosticSection, bool) {
	if len(svc.Events) == 0 {
		return DiagnosticSection{}, false
	}
	events := make([]DiagnosticEvent, 0, len(svc.Events))
	for _, e := range svc.Events {
		events = append(events, DiagnosticEvent{At: epochToRFC3339(e.CreatedAt), Message: e.Message})
	}
	return DiagnosticSection{
		ID:         "ecs-service-events",
		Title:      "ECS service events",
		Provenance: provenanceAWSAPI,
		Note:       "What the ECS scheduler recorded while the deployment was running.",
		Kind:       sectionKindEvents,
		Events:     events,
	}, true
}

// ecsDeploymentSection reports the state the rollout reached. Only the PRIMARY
// deployment is described: the one it superseded may well be the failure that
// prompted this deploy, and reporting its history here would read as this
// deploy's.
func ecsDeploymentSection(svc diagECSService) (DiagnosticSection, bool) {
	facts := []DiagnosticFact{
		{Label: "Desired count", Value: strconv.Itoa(svc.DesiredCount)},
		{Label: "Running count", Value: strconv.Itoa(svc.RunningCount)},
	}
	for _, d := range svc.Deployments {
		if d.Status != "PRIMARY" {
			continue
		}
		if d.RolloutState != "" {
			facts = append(facts, DiagnosticFact{Label: "Rollout state", Value: d.RolloutState})
		}
		if d.RolloutStateReason != "" {
			facts = append(facts, DiagnosticFact{Label: "Rollout reason", Value: d.RolloutStateReason})
		}
		if d.FailedTasks > 0 {
			facts = append(facts, DiagnosticFact{Label: "Failed tasks", Value: strconv.Itoa(d.FailedTasks)})
		}
		break
	}
	return DiagnosticSection{
		ID:         "ecs-deployment",
		Title:      "Deployment state",
		Provenance: provenanceAWSAPI,
		Kind:       sectionKindFacts,
		Facts:      facts,
	}, true
}

// ecsStoppedTasksSection is the half of the evidence that genuinely expires:
// the stopped task records, which ECS reaps an hour after the task stops and
// which the rollback's DeleteService takes with it long before that.
func ecsStoppedTasksSection(tasks []diagECSTask, omitted int) (DiagnosticSection, bool) {
	if len(tasks) == 0 {
		return DiagnosticSection{}, false
	}
	var facts []DiagnosticFact
	for _, t := range tasks {
		hint := "task " + shortTaskID(ecsTaskIDFromARN(t.TaskArn))
		if t.StopCode != "" {
			facts = append(facts, DiagnosticFact{Label: "Stop code", Value: t.StopCode, Hint: hint})
		}
		if t.StoppedReason != "" {
			facts = append(facts, DiagnosticFact{Label: "Stopped reason", Value: t.StoppedReason, Hint: hint})
		}
		for _, c := range t.Containers {
			if c.ExitCode != nil {
				facts = append(facts, DiagnosticFact{
					Label: "Exit code", Value: strconv.Itoa(*c.ExitCode),
					Hint: "container " + c.Name + ", " + hint,
				})
			}
			if c.Reason != "" {
				facts = append(facts, DiagnosticFact{
					Label: "Container reason", Value: c.Reason,
					Hint: "container " + c.Name + ", " + hint,
				})
			}
		}
	}
	if len(facts) == 0 {
		return DiagnosticSection{}, false
	}
	section := DiagnosticSection{
		ID:         "ecs-stopped-tasks",
		Title:      "Stopped tasks",
		Provenance: provenanceCapture,
		Note:       "Read from the task records before rollback deleted the service that owned them.",
		Kind:       sectionKindFacts,
		Facts:      facts,
	}
	if omitted > 0 {
		// Say what was left out rather than presenting a sample as the whole.
		section.Note += fmt.Sprintf(" %d further stopped task(s) reported the same way and are not shown.", omitted)
	}
	return section, true
}

// ── The two Overcast-authored sentences ────────────────────────────────────

// ecsHeadline is Overcast's reading of the evidence — the sentence the tab
// leads with. It is provenance overcast-inference wherever it is rendered, and
// it says nothing the facts above do not already contain; its whole job is to
// save the reader from assembling them.
//
// It returns "" rather than guessing when the stopped tasks carry no exit
// code, because a headline that says less than the panes below it is worse
// than no headline.
func ecsHeadline(tasks []diagECSTask, omitted int) string {
	container, exitCode, count, ran, ok := dominantECSExit(tasks)
	if !ok {
		return ""
	}

	headline := fmt.Sprintf("Container %q exited with code %d", container, exitCode)
	if ran > 0 {
		headline += " about " + approxDuration(ran) + " after starting"
	}
	switch {
	case omitted > 0:
		headline += fmt.Sprintf(", at least %d times", count+omitted)
	case count > 1:
		headline += fmt.Sprintf(", %d times", count)
	}
	return headline + "."
}

// dominantECSExit finds the container exit the headline should be about: the
// first container that reported an exit code, and how many of the captured
// tasks reported the same code for it. ran is the longest time that container's
// task stayed up, which is what makes "about 6s after starting" meaningful —
// an immediate exit and a slow crash need different fixes.
func dominantECSExit(tasks []diagECSTask) (container string, exitCode, count int, ran time.Duration, ok bool) {
	for _, t := range tasks {
		for _, c := range t.Containers {
			if c.ExitCode == nil {
				continue
			}
			if !ok {
				container, exitCode, ok = c.Name, *c.ExitCode, true
			}
			if c.Name != container || *c.ExitCode != exitCode {
				continue
			}
			count++
			if d, known := taskRuntime(t); known && d > ran {
				ran = d
			}
		}
	}
	return container, exitCode, count, ran, ok
}

// taskRuntime is how long a task stayed up. A task that never reached RUNNING
// has no startedAt, and reporting zero for it would claim an instant exit
// rather than a container that never got as far as exiting.
func taskRuntime(t diagECSTask) (time.Duration, bool) {
	if t.StartedAt == nil || t.StoppedAt == nil {
		return 0, false
	}
	seconds := *t.StoppedAt - *t.StartedAt
	if seconds < 0 {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

// ecsCounterfactual names what real AWS would have left the reader instead.
//
// It is the payload's honesty mechanism and it has to stay accurate, so each
// clause is added only when the corresponding evidence was actually gathered.
// The claims it makes are: AWS keeps the CloudFormation reason (always true);
// container output requires awslogs to have been configured on the task
// definition beforehand (true — a Fargate task with no log driver leaves
// nothing behind); and stopped task records are describable for about an hour
// after the task stops (true, and the reason Overcast copies them rather than
// pointing at them).
//
// It is omitted entirely when nothing was preserved — a capture that found
// only the service's own events has nothing AWS would have discarded, so there
// is no difference to teach and any sentence would be padding.
func ecsCounterfactual(awsReason string, haveContainerOutput, haveStoppedTasks bool) string {
	var preserved string
	switch {
	case haveContainerOutput:
		preserved = " The container output above exists because Overcast captured it before rollback — " +
			"in AWS it would require `awslogs` on the task definition."
	case haveStoppedTasks:
		preserved = " The stopped tasks above exist because Overcast captured them before rollback — " +
			"in AWS you would have had about an hour to run `aws ecs describe-tasks` before the records were reaped."
	default:
		return ""
	}
	if strings.TrimSpace(awsReason) == "" {
		return "In real AWS this deploy would have left you only CloudFormation's own record of which resource failed." + preserved
	}
	return "In real AWS this deploy would have left you only " + quoteReason(awsReason) + preserved
}

// ── Small formatting helpers ───────────────────────────────────────────────

// ecsTaskIDFromARN reads the task ID out of a task ARN
// (arn:aws:ecs:<region>:<account>:task/<cluster>/<id>).
func ecsTaskIDFromARN(taskARN string) string {
	if idx := strings.LastIndex(taskARN, "/"); idx >= 0 {
		return taskARN[idx+1:]
	}
	return taskARN
}

// ecsServiceNameFromARN reads the service name out of a service ARN
// (arn:aws:ecs:<region>:<account>:service/<cluster>/<name>). It returns ""
// rather than the whole string for anything that is not one, because the
// caller treats an empty name as "cannot tell whose tasks these are" and a
// plausible-looking wrong answer would defeat that.
func ecsServiceNameFromARN(serviceARN string) string {
	if idx := strings.LastIndex(serviceARN, "/"); idx >= 0 {
		return serviceARN[idx+1:]
	}
	return ""
}

// shortTaskID abbreviates a task ID the way the ECS console and CLI do, so a
// label reads as an identifier rather than as 32 characters of noise.
func shortTaskID(taskID string) string {
	if len(taskID) > 8 {
		return taskID[:8]
	}
	return taskID
}

// epochToRFC3339 converts ECS's fractional-second epoch timestamps. A zero (or
// absent) timestamp becomes an empty string rather than 1970.
func epochToRFC3339(epoch float64) string {
	if epoch <= 0 {
		return ""
	}
	sec := int64(epoch)
	nsec := int64((epoch - float64(sec)) * float64(time.Second))
	return time.Unix(sec, nsec).UTC().Format(time.RFC3339)
}

// approxDuration renders a duration at the precision a person would say it
// aloud. "about 6s" and "about 2m" are the useful readings; "6.0132s" is not,
// and implies a measurement this is not.
func approxDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return "under a second"
	case d < time.Minute:
		return strconv.Itoa(int(d.Round(time.Second)/time.Second)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Round(time.Minute)/time.Minute)) + "m"
	default:
		return strconv.Itoa(int(d.Round(time.Hour)/time.Hour)) + "h"
	}
}
