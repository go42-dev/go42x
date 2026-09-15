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

// Decode only the event fields that contribute to the generated instructions.
type githubEvent struct {
	Action     string `json:"action"`
	Number     int    `json:"number"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	PullRequest *githubSubject `json:"pull_request"`
	Issue       *githubSubject `json:"issue"`
	Comment     struct {
		Body string `json:"body"`
	} `json:"comment"`
	Review struct {
		Body string `json:"body"`
	} `json:"review"`
}

type githubSubject struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	// issue_comment events identify PR conversations through issue.pull_request.
	PullRequest *struct {
		HTMLURL string `json:"html_url"`
	} `json:"pull_request"`
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
	if repository != "" {
		repo["full_name"] = repository
	}
	owner := os.Getenv("GITHUB_REPOSITORY_OWNER")
	if owner == "" {
		if name, _, ok := strings.Cut(repository, "/"); ok {
			owner = name
		}
	}
	if owner != "" {
		repo["owner"] = owner
	}
	if len(repo) > 0 {
		result["repository"] = repo
	}
	event := make(map[string]any)
	for key, value := range map[string]string{
		"name":   os.Getenv("GITHUB_EVENT_NAME"),
		"action": payload.Action,
	} {
		if value != "" {
			event[key] = value
		}
	}
	if len(event) > 0 {
		result["event"] = event
	}
	if request := cmp.Or(payload.Comment.Body, payload.Review.Body); request != "" {
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
		for key, value := range map[string]string{"title": subject.Title, "body": subject.Body, "url": subject.HTMLURL} {
			if value != "" {
				details[key] = value
			}
		}
		if isPR {
			if subject.PullRequest != nil && subject.PullRequest.HTMLURL != "" {
				details["url"] = subject.PullRequest.HTMLURL
			}
			for key, value := range map[string]string{
				"head": cmp.Or(subject.Head.Ref, os.Getenv("GITHUB_HEAD_REF")),
				"base": cmp.Or(subject.Base.Ref, os.Getenv("GITHUB_BASE_REF")),
			} {
				if value != "" {
					details[key] = value
				}
			}
			result["pull_request"] = details
		} else {
			result["issue"] = details
		}
	}
	serverURL, runID := os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_RUN_ID")
	if serverURL != "" && repository != "" && runID != "" {
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
		if value := os.Getenv(env); value != "" {
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
			if value != "" {
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
	if payload := os.Getenv("GITHUB_EVENT_PAYLOAD"); payload != "" {
		if err := json.Unmarshal([]byte(payload), &event); err == nil {
			return event
		}
	}
	event = githubEvent{}
	if path := os.Getenv("GITHUB_EVENT_PATH"); path != "" {
		// #nosec G304 G703 -- The trusted runner supplies its event-file path; event payload content cannot select it.
		if data, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(data, &event); err == nil {
				return event
			}
		}
	}
	return githubEvent{}
}
