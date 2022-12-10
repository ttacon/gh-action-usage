package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"time"

	"github.com/google/go-github/v48/github"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
)

var (
	apiToken     = flag.String("api-token", "", "GitHub API token")
	orgName      = flag.String("org", "", "GitHub org name")
	numRepos     = flag.Int("num-repos", 25, "Number of repos to inspect")
	privateRepos = flag.Bool("private", true, "Private repos or public")
	isVerbose    = flag.Bool("is-verbose", false, "Run in verbose mode")
)

// repo -> workflow ID -> run -> usage
type WorkflowRunUsage struct {
	runID     int64
	runTiming int64
}

type WorkflowUsage struct {
	id   int64
	name string
	runs []*WorkflowRunUsage
}

func main() {
	flag.Parse()

	loggerConfig := zap.NewProductionConfig()
	loggerConfig.Encoding = "console"
	loggerConfig.DisableStacktrace = true
	loggerConfig.DisableCaller = true
	if *isVerbose {
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}

	logger, err := loggerConfig.Build()
	if err != nil {
		fmt.Println("failed to create logger, err: ", err)
		os.Exit(1)
	}
	lgr := logger.Sugar()
	lgr.Infoln("booting up client for analysis")

	var data = make(map[string]map[int64]WorkflowUsage)

	lgr.Debugln("constructing GitHub client")
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: *apiToken},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	repoType := "private"
	if len(*apiToken) == 0 {
		lgr.Debugln("no token provided, running in public mode - analysis is limited by GitHub rate limit")
		client = github.NewClient(nil)
	}

	if !*privateRepos {
		lgr.Infoln("only looking at public repositories")
		repoType = "public"
	}

	repos, _, err := client.Repositories.ListByOrg(ctx, *orgName, &github.RepositoryListByOrgOptions{
		Type:      repoType,
		Sort:      "pushed",
		Direction: "desc",
		ListOptions: github.ListOptions{
			PerPage: *numRepos,
		},
	})
	if err != nil {
		fmt.Println("failed to list repos, err:", err)
		os.Exit(1)
	}
	lgr.Infof("identified %d repositories to inspect", len(repos))

	date := time.Now().Add(-1 * time.Hour * 24 * 30).Format("2006-01-02")
	lgr.Infof("pulling workflow runs since: %q", date)

	for repoII, repo := range repos {
		repoWorkflowUsage := make(map[int64]WorkflowUsage)
		data[*repo.Name] = repoWorkflowUsage
		lgr.Infof("[%d/%d] checking %q", repoII, len(repos), *repo.Name)

		// list all repositories for the authenticated user
		workflows, resp, err := client.Actions.ListWorkflows(ctx, *orgName, *repo.Name, nil)
		if err != nil {
			if resp.StatusCode == 404 {
				continue
			}
			lgr.Error("failed to list workflows, err: ", err)

			os.Exit(1)
		}

		for _, workflow := range workflows.Workflows {
			wfUsage := WorkflowUsage{
				id:   *workflow.ID,
				name: *workflow.Name,
			}
			repoWorkflowUsage[*workflow.ID] = wfUsage
			runs, resp, err := client.Actions.ListWorkflowRunsByID(
				ctx,
				*orgName,
				*repo.Name,
				*workflow.ID,
				&github.ListWorkflowRunsOptions{
					Created: fmt.Sprintf(">=%s", date),
				},
			)
			if err != nil {
				if resp.StatusCode == 404 {
					continue
				}
				lgr.Error("failed to get run, err: ", err)
				continue
			}
			for _, run := range runs.WorkflowRuns {
				usage, resp, err := client.Actions.GetWorkflowRunUsageByID(
					ctx,
					*orgName,
					*repo.Name,
					*run.ID,
				)
				if err != nil {
					if resp.StatusCode == 404 {
						continue
					}
					lgr.Error("failed to get run usage, err:", err)
					continue
				} else if usage == nil || usage.RunDurationMS == nil {
					continue
				}

				var billableMS int64
				if billable := usage.GetBillable(); billable != nil {
					if platform := billable.GetMacOS(); platform != nil {
						billableMS += platform.GetTotalMS()
					}
					if platform := billable.GetUbuntu(); platform != nil {
						billableMS += platform.GetTotalMS()
					}
					if platform := billable.GetWindows(); platform != nil {
						billableMS += platform.GetTotalMS()
					}
				}

				wfUsage.runs = append(wfUsage.runs, &WorkflowRunUsage{
					runID:     *run.ID,
					runTiming: billableMS,
				})
			}
			repoWorkflowUsage[*workflow.ID] = wfUsage
		}
	}

	lgr.Info("data retrieval completed")

	buf := bytes.NewBuffer(nil)
	csvOut := csv.NewWriter(buf)
	for repoName, usage := range data {
		for _, usge := range usage {
			var runTotal int64 = 0
			numRuns := len(usge.runs)
			if numRuns == 0 {
				continue
			}
			for _, run := range usge.runs {
				runTotal += run.runTiming
			}

			avgRun := int(runTotal) / numRuns
			csvOut.Write([]string{
				repoName,
				strconv.FormatInt(usge.id, 10),
				usge.name,
				strconv.Itoa(numRuns),
				strconv.FormatInt(runTotal, 10),
				strconv.Itoa(avgRun),
			})
		}
	}
	csvOut.Flush()

	fileName := fmt.Sprintf("gh-action-usage-%s-%s.csv", *orgName, date)

	if err := ioutil.WriteFile(fileName, buf.Bytes(), os.ModePerm); err != nil {
		lgr.Error("failed to write file, err:", err)
	}

	lgr.Infof("data written to %q", fileName)

}
