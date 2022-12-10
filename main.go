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
	"golang.org/x/oauth2"
)

var (
	apiToken = flag.String("api-token", "", "GitHub API token")
	orgName  = flag.String("org", "", "GitHub org name")
)

// repo -> workflow ID -> run -> usage
type WorkflowRunUsage struct {
	runID int64 
	runTiming int64
}

type WorkflowUsage struct {
	id int64
	name string
	runs []*WorkflowRunUsage
}

func main() {
	flag.Parse()
	fmt.Println("booting up")

	var data = make(map[string]map[int64]WorkflowUsage)

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: *apiToken},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	repos, _, err := client.Repositories.ListByOrg(ctx, *orgName, &github.RepositoryListByOrgOptions{
		Type: "private",
		Sort: "pushed",
		Direction: "desc",
		ListOptions: github.ListOptions{
			PerPage: 50,
		},
	})
	if err != nil {
		fmt.Println("failed to list repos, err:", err)
		os.Exit(1)
	}

	for repoII, repo := range repos {
		repoWorkflowUsage := make(map[int64]WorkflowUsage)
		data[*repo.Name] = repoWorkflowUsage
		fmt.Printf("[%d/%d] checking %q\n", repoII, len(repos), *repo.Name)

	// list all repositories for the authenticated user
	workflows, resp, err := client.Actions.ListWorkflows(ctx, *orgName, *repo.Name, nil)
	if err != nil {
		if resp.StatusCode == 404 {
			continue
		}
		fmt.Println("failed to list workflows, err: ", err)
		
		os.Exit(1)
	}


	date := time.Now().Add(-1*time.Hour*24*30).Format("2006-01-02")
	fmt.Printf("pulling workflow runs since: %q\n", date)

	for _, workflow := range workflows.Workflows {
		wfUsage := WorkflowUsage{
			id: *workflow.ID,
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
			fmt.Println("failed to get run, err: ", err)
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
				fmt.Println("failed to get run usage, err:",err)
				continue
			} else if usage == nil || usage.RunDurationMS == nil {
				fmt.Println("early exit")
				continue
			}

			// dataJSON, _ := json.Marshal(usage)
			// fmt.Println(string(dataJSON))
			
			// fmt.Println(wfUsage.runs)
			var billableMS int64 
			if billable := usage.GetBillable(); billable != nil  {
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
				runID: *run.ID,
				runTiming: billableMS,
			})
			
			// fmt.Println(wfUsage.runs)
		}
		repoWorkflowUsage[*workflow.ID] = wfUsage
	}
	}

	// fmt.Printf("%v\n", data)
	// raw, _ := json.MarshalIndent(data, "", "  ")
	// fmt.Println(string(raw))

	buf := bytes.NewBuffer(nil)
	csvOut := csv.NewWriter(buf)
	for repoName, usage := range data {
		fmt.Println("----------")
		fmt.Println(repoName)
		for _, usge := range usage {
			fmt.Printf("[%d] %s: ", usge.id, usge.name)
			var runTotal int64 = 0
			numRuns := len(usge.runs)
			if numRuns == 0 {
				fmt.Println("0 runs")
				continue
			} 
			for _, run := range usge.runs {
				runTotal += run.runTiming
			}

			avgRun := int(runTotal)/numRuns
			fmt.Printf(
				"num runs: %d, total time: %d, avg time: %d\n",
				numRuns,
				runTotal,
				avgRun,
			)
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
	if err := ioutil.WriteFile("gh-action-usage.csv", buf.Bytes(), os.ModePerm); err != nil {
		fmt.Println("failed to write file, err:", err)
	}

}