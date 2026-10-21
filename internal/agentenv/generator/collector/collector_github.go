package collector

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const GitHubActionsCollectorName = "github_actions"

// GitHubActionsCollector collects workflow metadata and event task context.
type GitHubActionsCollector struct {
	BaseCollector
}

func NewGitHubActionsCollector() *GitHubActionsCollector {
	return &GitHubActionsCollector{
		BaseCollector: NewBaseCollector(GitHubActionsCollectorName, 30),
	}
}

// githubEvent contains only event fields used in generated instructions.
type githubEvent struct {
	Action      string           `json:"action"`
	Number      int              `json:"number"`
	Repository  githubRepository `json:"repository"`
	PullRequest *githubSubject   `json:"pull_request"`
	Issue       *githubSubject   `json:"issue"`
	Comment     githubMessage    `json:"comment"`
	Review      githubMessage    `json:"review"`
}

type githubRepository struct {
	FullName string `json:"full_name"`
}

type githubMessage struct {
	Body string `json:"body"`
}

type githubBranch struct {
	Ref string `json:"ref"`
}

type githubPullRequestLink struct {
	HTMLURL string `json:"html_url"`
}

type githubSubject struct {
	Number  int          `json:"number"`
	Title   string       `json:"title"`
	Body    string       `json:"body"`
	HTMLURL string       `json:"html_url"`
	Head    githubBranch `json:"head"`
	Base    githubBranch `json:"base"`
	// issue_comment events identify PR conversations through issue.pull_request.
	PullRequest *githubPullRequestLink `json:"pull_request"`
}

func (c *GitHubActionsCollector) Collect(_ context.Context) (map[string]any, error) {
	result := make(map[string]any)
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return result, nil
	}

	c.collectWorkflowContext(result)

	payload := readGitHubEvent()
	repository := cmp.Or(os.Getenv("GITHUB_REPOSITORY"), payload.Repository.FullName)
	repo := make(map[string]any)
	if len(repository) > 0 {
		repo["full_name"] = repository
	}
	owner := os.Getenv("GITHUB_REPOSITORY_OWNER")
	if len(owner) == 0 {
		if name, _, ok := strings.Cut(repository, "/"); ok {
			owner = name
		}
	}
	if len(owner) > 0 {
		repo["owner"] = owner
	}
	if len(repo) > 0 {
		result["repository"] = repo
	}
	event := make(map[string]any)
	if name := os.Getenv("GITHUB_EVENT_NAME"); len(name) > 0 {
		event["name"] = name
	}
	if len(payload.Action) > 0 {
		event["action"] = payload.Action
	}
	if len(event) > 0 {
		result["event"] = event
	}
	if request := cmp.Or(payload.Comment.Body, payload.Review.Body); len(request) > 0 {
		result["user_request"] = request
	}

	subject, isPR := payload.PullRequest, payload.PullRequest != nil
	if subject == nil && payload.Issue != nil {
		subject = payload.Issue
		isPR = subject.PullRequest != nil
	}
	if subject != nil {
		details := map[string]any{"is_pr": isPR}
		if number := cmp.Or(subject.Number, payload.Number); number > 0 {
			details["number"] = number
		}
		if len(subject.Title) > 0 {
			details["title"] = subject.Title
		}
		if len(subject.Body) > 0 {
			details["body"] = subject.Body
		}
		if len(subject.HTMLURL) > 0 {
			details["url"] = subject.HTMLURL
		}
		if isPR {
			if subject.PullRequest != nil && len(subject.PullRequest.HTMLURL) > 0 {
				details["url"] = subject.PullRequest.HTMLURL
			}
			if head := cmp.Or(subject.Head.Ref, os.Getenv("GITHUB_HEAD_REF")); len(head) > 0 {
				details["head"] = head
			}
			if base := cmp.Or(subject.Base.Ref, os.Getenv("GITHUB_BASE_REF")); len(base) > 0 {
				details["base"] = base
			}
			result["pull_request"] = details
		} else {
			result["issue"] = details
		}
	}
	serverURL, runID := os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_RUN_ID")
	if len(serverURL) > 0 && len(repository) > 0 && len(runID) > 0 {
		result["build_url"] = fmt.Sprintf("%s/%s/actions/runs/%s", strings.TrimRight(serverURL, "/"), repository, runID)
	}

	return result, nil
}

func (c *GitHubActionsCollector) collectWorkflowContext(result map[string]any) {
	for key, env := range map[string]string{
		"action":           "GITHUB_ACTION",
		"action_path":      "GITHUB_ACTION_PATH",
		"api_url":          "GITHUB_API_URL",
		"base_ref":         "GITHUB_BASE_REF",
		"event_name":       "GITHUB_EVENT_NAME",
		"event_path":       "GITHUB_EVENT_PATH",
		"head_ref":         "GITHUB_HEAD_REF",
		"job":              "GITHUB_JOB",
		"ref":              "GITHUB_REF",
		"ref_name":         "GITHUB_REF_NAME",
		"ref_type":         "GITHUB_REF_TYPE",
		"repository_owner": "GITHUB_REPOSITORY_OWNER",
		"run_id":           "GITHUB_RUN_ID",
		"run_number":       "GITHUB_RUN_NUMBER",
		"run_attempt":      "GITHUB_RUN_ATTEMPT",
		"sha":              "GITHUB_SHA",
		"workspace":        "GITHUB_WORKSPACE",
		"server_url":       "GITHUB_SERVER_URL",
	} {
		if value := os.Getenv(env); len(value) > 0 {
			result[key] = value
		}
	}

	for key, fields := range map[string]map[string]string{
		"actor": {
			"login":            os.Getenv("GITHUB_ACTOR"),
			"triggering_actor": os.Getenv("GITHUB_TRIGGERING_ACTOR"),
		},
		"workflow": {
			"name": os.Getenv("GITHUB_WORKFLOW"),
			"ref":  os.Getenv("GITHUB_WORKFLOW_REF"),
			"sha":  os.Getenv("GITHUB_WORKFLOW_SHA"),
		},
		"runner": {
			"name":       os.Getenv("RUNNER_NAME"),
			"os":         os.Getenv("RUNNER_OS"),
			"arch":       os.Getenv("RUNNER_ARCH"),
			"temp_dir":   os.Getenv("RUNNER_TEMP"),
			"tool_cache": os.Getenv("RUNNER_TOOL_CACHE"),
		},
	} {
		group := make(map[string]any)
		for name, value := range fields {
			if len(value) > 0 {
				group[name] = value
			}
		}
		if len(group) > 0 {
			result[key] = group
		}
	}
}

func readGitHubEvent() githubEvent {
	var event githubEvent
	// ai-prepare-env passes the payload inline; standard Actions event files are a fallback.
	if payload := os.Getenv("GITHUB_EVENT_PAYLOAD"); len(payload) > 0 {
		if err := json.Unmarshal([]byte(payload), &event); err == nil {
			return event
		}
	}
	event = githubEvent{}
	if path := os.Getenv("GITHUB_EVENT_PATH"); len(path) > 0 {
		// #nosec G304 G703 -- The trusted runner supplies its event-file path; event payload content cannot select it.
		if data, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(data, &event); err == nil {
				return event
			}
		}
	}
	return githubEvent{}
}
