package ghactionusage

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/google/go-github/v48/github"
)

type GitHubOrg interface {
	ListRepos(
		ctx context.Context,
		repoType string,
		limit int,
	) ([]*github.Repository, error)
	ListWorkflowsForRepo(ctx context.Context, repo *github.Repository) (*github.Workflows, error)
	ListRunsForWorkflow(ctx context.Context, repo *github.Repository, wkflw *github.Workflow, since time.Time) (*github.WorkflowRuns, error)
	ListUsageForWorkflowRun(ctx context.Context, repo *github.Repository, run *github.WorkflowRun) (*github.WorkflowRunUsage, error)
}

type ghOrg struct {
	client  *github.Client
	orgName string
}

func NewGHOrg(client *github.Client, orgName string) GitHubOrg {
	return &ghOrg{
		client:  client,
		orgName: orgName,
	}
}

func (c *ghOrg) ListRepos(
	ctx context.Context,
	repoType string,
	limit int,
) ([]*github.Repository, error) {
	repos, resp, err := c.client.Repositories.ListByOrg(
		ctx,
		c.orgName,
		&github.RepositoryListByOrgOptions{
			Type:      repoType,
			Sort:      "pushed",
			Direction: "desc",
			ListOptions: github.ListOptions{
				PerPage: limit,
			},
		},
	)
	if err != nil {
		return nil, err
	} else if err := resp.Body.Close(); err != nil {
		return nil, err
	}

	return repos, nil
}

func (c *ghOrg) ListWorkflowsForRepo(ctx context.Context, repo *github.Repository) (*github.Workflows, error) {
	workflows, resp, err := c.client.Actions.ListWorkflows(
		ctx,
		c.orgName,
		*repo.Name,
		nil,
	)
	if err != nil {
		if resp.StatusCode == 404 {
			return nil, nil
		}
		return nil, err
	} else if err := resp.Body.Close(); err != nil {
		return nil, err
	}
	return workflows, nil
}
func (c *ghOrg) ListRunsForWorkflow(
	ctx context.Context,
	repo *github.Repository,
	wkflw *github.Workflow,
	since time.Time,
) (*github.WorkflowRuns, error) {
	runs, resp, err := c.client.Actions.ListWorkflowRunsByID(
		ctx,
		c.orgName,
		*repo.Name,
		*wkflw.ID,
		&github.ListWorkflowRunsOptions{
			Created: fmt.Sprintf(">=%s", since),
		},
	)
	if err != nil {
		if resp.StatusCode == 404 {
			return nil, nil
		}
		return nil, err
	} else if err := resp.Body.Close(); err != nil {
		return nil, err
	}
	return runs, nil
}
func (c *ghOrg) ListUsageForWorkflowRun(
	ctx context.Context,
	repo *github.Repository,
	run *github.WorkflowRun,
) (*github.WorkflowRunUsage, error) {
	usage, resp, err := c.client.Actions.GetWorkflowRunUsageByID(
		ctx,
		c.orgName,
		*repo.Name,
		*run.ID,
	)
	if err != nil {
		if resp.StatusCode == 404 {
			return nil, nil
		}
		return nil, err
	} else if err := resp.Body.Close(); err != nil {
		return nil, err
	}
	return usage, nil
}

func Run(
	ctx context.Context,
	ghOrg GitHubOrg,
	repoType string,
	limit int,
	since time.Time,
) (*UsageData, error) {
	repos, err := ghOrg.ListRepos(ctx, repoType, limit)
	if err != nil {
		return nil, err
	}

	usg := new(UsageData)

	for _, repo := range repos {
		repoData := new(RepoWorkflowUsageData)
		repoData.Repo = repo
		usg.Data = append(usg.Data, repoData)

		workflows, err := ghOrg.ListWorkflowsForRepo(ctx, repo)
		if err != nil {
			return nil, err
		}

		for _, workflow := range workflows.Workflows {
			wkflwData := new(WorkflowUsageData)
			wkflwData.Workflow = workflow
			repoData.Data = append(repoData.Data, wkflwData)

			runs, err := ghOrg.ListRunsForWorkflow(ctx, repo, workflow, since)
			if err != nil {
				return nil, err
			}

			for _, run := range runs.WorkflowRuns {
				runData := new(WorkflowRunUsageData)
				runData.Run = run
				wkflwData.Data = append(wkflwData.Data, runData)

				usage, err := ghOrg.ListUsageForWorkflowRun(ctx, repo, run)
				if err != nil {
					return nil, err
				}

				runData.Data = usage
			}
		}
	}

	return usg, nil
}

type UsageData struct {
	Data []*RepoWorkflowUsageData
}

type RepoWorkflowUsageData struct {
	Repo *github.Repository
	Data []*WorkflowUsageData
}

type WorkflowUsageData struct {
	Workflow *github.Workflow
	Data     []*WorkflowRunUsageData
}

type WorkflowRunUsageData struct {
	Run  *github.WorkflowRun
	Data *github.WorkflowRunUsage
}

func getNillableWorkflowRunBill(b *github.WorkflowRunBill) int64 {
	if b == nil {
		return 0
	}
	return b.GetTotalMS()
}

func (u *UsageData) WriteRawToCSV(w io.Writer) error {
	csvOut := csv.NewWriter(w)

	for _, repoData := range u.Data {
		for _, workflow := range repoData.Data {
			for _, run := range workflow.Data {
				csvOut.Write([]string{
					repoData.Repo.GetName(),
					strconv.FormatInt(workflow.Workflow.GetID(), 10),
					workflow.Workflow.GetName(),
					strconv.FormatInt(run.Run.GetID(), 10),
					strconv.FormatInt(
						getNillableWorkflowRunBill(run.Data.Billable.GetMacOS()),
						10,
					),
					strconv.FormatInt(
						getNillableWorkflowRunBill(run.Data.Billable.GetUbuntu()),
						10,
					),
					strconv.FormatInt(
						getNillableWorkflowRunBill(run.Data.Billable.GetWindows()),
						10,
					),
				})
			}
		}
	}

	csvOut.Flush()
	return csvOut.Error()

}
