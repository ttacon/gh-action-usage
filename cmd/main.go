package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/google/go-github/v48/github"
	ghactionusage "github.com/ttacon/gh-action-usage"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
)

var (
	apiToken     = flag.String("api-token", "", "GitHub API token")
	orgName      = flag.String("org", "", "GitHub org name")
	numRepos     = flag.Int("num-repos", 25, "Number of repos to inspect")
	numDays      = flag.Int("num-days", 5, "The number of days to inspect usage for")
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

	lgr.Debugln("constructing GitHub client")
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: *apiToken},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	repoType := "private"

	date := time.Now().Add(-1 * time.Hour * time.Duration(*numDays) * 30)

	ghOrg := ghactionusage.NewGHOrg(client, *orgName)
	lgr.Info("beginning data retrieval")
	data, err := ghactionusage.Run(
		ctx,
		ghOrg,
		repoType,
		*numRepos,
		date,
	)

	lgr.Info("data retrieval completed")

	buf := bytes.NewBuffer(nil)

	if err := data.WriteRawToCSV(buf); err != nil {
		lgr.Error("failed to write usage data, err: ", err)
		os.Exit(1)
	}

	dateStr := date.Format("2006-01-02")
	fileName := fmt.Sprintf("gh-action-usage-%s-%s.csv", *orgName, dateStr)

	if err := ioutil.WriteFile(fileName, buf.Bytes(), os.ModePerm); err != nil {
		lgr.Error("failed to write file, err:", err)
	}

	lgr.Infof("data written to %q", fileName)

}
