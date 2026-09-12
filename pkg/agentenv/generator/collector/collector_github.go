package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

const GitHubActionsCollectorName = "github_actions"

// GitHubActionsCollector collects GitHub Actions workflow context
type GitHubActionsCollector struct {
	BaseCollector
}

func NewGitHubActionsCollector() *GitHubActionsCollector {
	return &GitHubActionsCollector{
		BaseCollector: NewBaseCollector(GitHubActionsCollectorName, 30),
	}
}

func (c *GitHubActionsCollector) Collect(_ context.Context) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return result, nil
	}

	c.collectBasicInfo(result)
	c.collectRepositoryInfo(result)
	c.collectEventInfo(result)
	c.collectPRIssueInfo(result)
	c.collectActorInfo(result)
	c.collectWorkflowInfo(result)
	c.collectRunnerInfo(result)
	c.derivatives(result)

	return result, nil
}

func (c *GitHubActionsCollector) collectBasicInfo(result map[string]interface{}) {
	envVars := map[string]string{
		"action":           os.Getenv("GITHUB_ACTION"),
		"action_path":      os.Getenv("GITHUB_ACTION_PATH"),
		"actor":            os.Getenv("GITHUB_ACTOR"),
		"api_url":          os.Getenv("GITHUB_API_URL"),
		"base_ref":         os.Getenv("GITHUB_BASE_REF"),
		"event_name":       os.Getenv("GITHUB_EVENT_NAME"),
		"event_path":       os.Getenv("GITHUB_EVENT_PATH"),
		"head_ref":         os.Getenv("GITHUB_HEAD_REF"),
		"job":              os.Getenv("GITHUB_JOB"),
		"ref":              os.Getenv("GITHUB_REF"),
		"ref_name":         os.Getenv("GITHUB_REF_NAME"),
		"ref_type":         os.Getenv("GITHUB_REF_TYPE"),
		"repository":       os.Getenv("GITHUB_REPOSITORY"),
		"repository_owner": os.Getenv("GITHUB_REPOSITORY_OWNER"),
		"run_id":           os.Getenv("GITHUB_RUN_ID"),
		"run_number":       os.Getenv("GITHUB_RUN_NUMBER"),
		"run_attempt":      os.Getenv("GITHUB_RUN_ATTEMPT"),
		"sha":              os.Getenv("GITHUB_SHA"),
		"workflow":         os.Getenv("GITHUB_WORKFLOW"),
		"workspace":        os.Getenv("GITHUB_WORKSPACE"),
		"server_url":       os.Getenv("GITHUB_SERVER_URL"),
	}
	for key, value := range envVars {
		if value != "" {
			result[key] = value
		}
	}
}

func (c *GitHubActionsCollector) collectRepositoryInfo(result map[string]interface{}) {
	repo := make(map[string]interface{})

	if r := os.Getenv("REPOSITORY"); r != "" {
		repo["full_name"] = r
	} else if r := os.Getenv("GITHUB_REPOSITORY"); r != "" {
		repo["full_name"] = r
	}

	if owner := os.Getenv("GITHUB_REPOSITORY_OWNER"); owner != "" {
		repo["owner"] = owner
	}

	if len(repo) > 0 {
		result["repository"] = repo
	}
}

func (c *GitHubActionsCollector) collectEventInfo(result map[string]interface{}) {
	event := make(map[string]interface{})

	if eventName := os.Getenv("EVENT_NAME"); eventName != "" {
		event["name"] = eventName
	} else if eventName := os.Getenv("GITHUB_EVENT_NAME"); eventName != "" {
		event["name"] = eventName
	}

	// Parse event payload if available
	if eventPayload := os.Getenv("GITHUB_EVENT_PAYLOAD"); eventPayload != "" {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(eventPayload), &payload); err == nil {
			event["payload"] = payload
		} else {
			event["payload_raw"] = eventPayload
		}
	}

	// Try to read event from file if path is provided
	if eventPath := os.Getenv("GITHUB_EVENT_PATH"); eventPath != "" {
		if data, err := os.ReadFile(eventPath); err == nil {
			var payload map[string]interface{}
			if err := json.Unmarshal(data, &payload); err == nil {
				if _, exists := event["payload"]; !exists {
					event["payload"] = payload
				}
			}
		}
	}

	if len(event) > 0 {
		result["event"] = event
	}
}

func (c *GitHubActionsCollector) collectPRIssueInfo(result map[string]interface{}) {
	// Check if this is a PR
	isPR := false
	if pr := os.Getenv("IS_PR"); pr != "" {
		isPR, _ = strconv.ParseBool(pr)
	}

	// Issue/PR number
	var number int
	if n := os.Getenv("ISSUE_NUMBER"); n != "" {
		number, _ = strconv.Atoi(n)
	}

	if isPR {
		pr := make(map[string]interface{})
		pr["number"] = number
		pr["is_pr"] = true

		if title := os.Getenv("PR_TITLE"); title != "" {
			pr["title"] = title
		}
		if body := os.Getenv("PR_BODY"); body != "" {
			pr["body"] = body
		}
		if base := os.Getenv("PR_BASE"); base != "" {
			pr["base"] = base
		}
		if head := os.Getenv("PR_HEAD"); head != "" {
			pr["head"] = head
		}

		result["pull_request"] = pr
	} else if number > 0 {
		issue := make(map[string]interface{})
		issue["number"] = number
		issue["is_pr"] = false

		result["issue"] = issue
	}

	// User request (comment or review body)
	if request := os.Getenv("USER_REQUEST"); request != "" {
		result["user_request"] = request
	}
}

func (c *GitHubActionsCollector) collectActorInfo(result map[string]interface{}) {
	actor := make(map[string]interface{})

	if a := os.Getenv("ACTOR"); a != "" {
		actor["login"] = a
	} else if a := os.Getenv("GITHUB_ACTOR"); a != "" {
		actor["login"] = a
	}

	if triggeredBy := os.Getenv("GITHUB_TRIGGERING_ACTOR"); triggeredBy != "" {
		actor["triggering_actor"] = triggeredBy
	}

	if len(actor) > 0 {
		result["actor"] = actor
	}
}

func (c *GitHubActionsCollector) collectWorkflowInfo(result map[string]interface{}) {
	workflow := make(map[string]interface{})

	if w := os.Getenv("GITHUB_WORKFLOW"); w != "" {
		workflow["name"] = w
	}

	if ref := os.Getenv("GITHUB_WORKFLOW_REF"); ref != "" {
		workflow["ref"] = ref
	}

	if sha := os.Getenv("GITHUB_WORKFLOW_SHA"); sha != "" {
		workflow["sha"] = sha
	}

	if len(workflow) > 0 {
		result["workflow"] = workflow
	}
}

func (c *GitHubActionsCollector) collectRunnerInfo(result map[string]interface{}) {
	runner := make(map[string]interface{})

	if name := os.Getenv("RUNNER_NAME"); name != "" {
		runner["name"] = name
	}

	if osInfo := os.Getenv("RUNNER_OS"); osInfo != "" {
		runner["os"] = osInfo
	}

	if arch := os.Getenv("RUNNER_ARCH"); arch != "" {
		runner["arch"] = arch
	}

	if temp := os.Getenv("RUNNER_TEMP"); temp != "" {
		runner["temp_dir"] = temp
	}

	if toolCache := os.Getenv("RUNNER_TOOL_CACHE"); toolCache != "" {
		runner["tool_cache"] = toolCache
	}

	if len(runner) > 0 {
		result["runner"] = runner
	}
}

func (c *GitHubActionsCollector) derivatives(result map[string]interface{}) {
	serverURL, _ := result["server_url"].(string)
	repository, _ := result["repository"].(map[string]interface{})
	fullName, _ := repository["full_name"].(string)
	runID, _ := result["run_id"].(string)
	if serverURL != "" && fullName != "" && runID != "" {
		result["build_url"] = fmt.Sprintf("%s/%s/actions/runs/%s", serverURL, fullName, runID)
	}
}
