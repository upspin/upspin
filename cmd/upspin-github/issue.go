// Copyright 2015 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

var (
	project      = flag.String("p", "golang/go", "GitHub owner/repo name")
	rawFlag      = flag.Bool("raw", false, "do no processing of markdown")
	tokenFile    = flag.String("token", "", "read GitHub token personal access token from `file` (default $HOME/.github-issue-token)")
	projectOwner = ""
	projectRepo  = ""
)

func initIssue() {
	f := strings.Split(*project, "/")
	if len(f) != 2 {
		log.Fatal("invalid form for -p argument: must be owner/repo, like golang/go")
	}
	projectOwner = f[0]
	projectRepo = f[1]

	loadAuth()
}

func showIssue(w io.Writer, n int) (*github.Issue, error) {
	issue, _, err := client.Issues.Get(projectOwner, projectRepo, n)
	if err != nil {
		return nil, err
	}
	updateIssueCache(issue)
	return issue, printIssue(w, issue)
}

const timeFormat = "2006-01-02 15:04:05"

func printIssue(w io.Writer, issue *github.Issue) error {
	fmt.Fprintf(w, "Title: %s\n", getString(issue.Title))
	fmt.Fprintf(w, "State: %s\n", getString(issue.State))
	fmt.Fprintf(w, "Assignee: %s\n", getUserLogin(issue.Assignee))
	if issue.ClosedAt != nil {
		fmt.Fprintf(w, "Closed: %s\n", getTime(issue.ClosedAt).Format(timeFormat))
	}
	fmt.Fprintf(w, "Labels: %s\n", strings.Join(getLabelNames(issue.Labels), " "))
	fmt.Fprintf(w, "Milestone: %s\n", getMilestoneTitle(issue.Milestone))
	fmt.Fprintf(w, "URL: https://github.com/%s/%s/issues/%d\n", projectOwner, projectRepo, getInt(issue.Number))

	fmt.Fprintf(w, "\nReported by %s (%s)\n", getUserLogin(issue.User), getTime(issue.CreatedAt).Format(timeFormat))
	if issue.Body != nil {
		if *rawFlag {
			fmt.Fprintf(w, "\n%s\n\n", *issue.Body)
		} else {
			text := strings.TrimSpace(*issue.Body)
			if text != "" {
				fmt.Fprintf(w, "\n\t%s\n", wrap(text, "\t"))
			}
		}
	}

	var output []string

	for page := 1; ; {
		list, resp, err := client.Issues.ListComments(projectOwner, projectRepo, getInt(issue.Number), &github.IssueListCommentsOptions{
			ListOptions: github.ListOptions{
				Page:    page,
				PerPage: 100,
			},
		})
		for _, com := range list {
			var buf bytes.Buffer
			w := &buf
			fmt.Fprintf(w, "%s\n", getTime(com.CreatedAt).Format(time.RFC3339))
			fmt.Fprintf(w, "\nComment by %s (%s)\n", getUserLogin(com.User), getTime(com.CreatedAt).Format(timeFormat))
			if com.Body != nil {
				if *rawFlag {
					fmt.Fprintf(w, "\n%s\n\n", *com.Body)
				} else {
					text := strings.TrimSpace(*com.Body)
					if text != "" {
						fmt.Fprintf(w, "\n\t%s\n", wrap(text, "\t"))
					}
				}
			}
			output = append(output, buf.String())
		}
		if err != nil {
			return err
		}
		if resp.NextPage < page {
			break
		}
		page = resp.NextPage
	}

	for page := 1; ; {
		list, resp, err := client.Issues.ListIssueEvents(projectOwner, projectRepo, getInt(issue.Number), &github.ListOptions{
			Page:    page,
			PerPage: 100,
		})
		for _, ev := range list {
			var buf bytes.Buffer
			w := &buf
			fmt.Fprintf(w, "%s\n", getTime(ev.CreatedAt).Format(time.RFC3339))
			switch event := getString(ev.Event); event {
			case "mentioned", "subscribed", "unsubscribed":
				// ignore
			default:
				fmt.Fprintf(w, "\n* %s %s (%s)\n", getUserLogin(ev.Actor), event, getTime(ev.CreatedAt).Format(timeFormat))
			case "closed", "referenced", "merged":
				id := getString(ev.CommitID)
				if id != "" {
					if len(id) > 7 {
						id = id[:7]
					}
					id = " in commit " + id
				}
				fmt.Fprintf(w, "\n* %s %s%s (%s)\n", getUserLogin(ev.Actor), event, id, getTime(ev.CreatedAt).Format(timeFormat))
				if id != "" {
					commit, _, err := client.Git.GetCommit(projectOwner, projectRepo, *ev.CommitID)
					if err == nil {
						fmt.Fprintf(w, "\n\tAuthor: %s <%s> %s\n\tCommitter: %s <%s> %s\n\n\t%s\n",
							getString(commit.Author.Name), getString(commit.Author.Email), getTime(commit.Author.Date).Format(timeFormat),
							getString(commit.Committer.Name), getString(commit.Committer.Email), getTime(commit.Committer.Date).Format(timeFormat),
							wrap(getString(commit.Message), "\t"))
					}
				}
			case "assigned", "unassigned":
				fmt.Fprintf(w, "\n* %s %s %s (%s)\n", getUserLogin(ev.Actor), event, getUserLogin(ev.Assignee), getTime(ev.CreatedAt).Format(timeFormat))
			case "labeled", "unlabeled":
				fmt.Fprintf(w, "\n* %s %s %s (%s)\n", getUserLogin(ev.Actor), event, getString(ev.Label.Name), getTime(ev.CreatedAt).Format(timeFormat))
			case "milestoned", "demilestoned":
				if event == "milestoned" {
					event = "added to milestone"
				} else {
					event = "removed from milestone"
				}
				fmt.Fprintf(w, "\n* %s %s %s (%s)\n", getUserLogin(ev.Actor), event, getString(ev.Milestone.Title), getTime(ev.CreatedAt).Format(timeFormat))
			case "renamed":
				fmt.Fprintf(w, "\n* %s changed title (%s)\n  - %s\n  + %s\n", getUserLogin(ev.Actor), getTime(ev.CreatedAt).Format(timeFormat), getString(ev.Rename.From), getString(ev.Rename.To))
			}
			output = append(output, buf.String())
		}
		if err != nil {
			return err
		}
		if resp.NextPage < page {
			break
		}
		page = resp.NextPage
	}

	sort.Strings(output)
	for _, s := range output {
		i := strings.Index(s, "\n")
		fmt.Fprintf(w, "%s", s[i+1:])
	}

	return nil
}

func showQuery(w io.Writer, q string) error {
	all, err := searchIssues(q)
	if err != nil {
		return err
	}
	sort.Sort(issuesByTitle(all))
	for _, issue := range all {
		fmt.Fprintf(w, "%v\t%v\n", getInt(issue.Number), getString(issue.Title))
	}
	return nil
}

type issuesByTitle []*github.Issue

func (x issuesByTitle) Len() int      { return len(x) }
func (x issuesByTitle) Swap(i, j int) { x[i], x[j] = x[j], x[i] }
func (x issuesByTitle) Less(i, j int) bool {
	if getString(x[i].Title) != getString(x[j].Title) {
		return getString(x[i].Title) < getString(x[j].Title)
	}
	return getInt(x[i].Number) < getInt(x[j].Number)
}

func searchIssues(q string) ([]*github.Issue, error) {
	if opt, ok := queryToListOptions(q); ok {
		return listRepoIssues(opt)
	}

	var all []*github.Issue
	for page := 1; ; {
		// TODO(rsc): Rethink excluding pull requests.
		x, resp, err := client.Search.Issues("type:issue state:open repo:"+*project+" "+q, &github.SearchOptions{
			ListOptions: github.ListOptions{
				Page:    page,
				PerPage: 100,
			},
		})
		for i := range x.Issues {
			updateIssueCache(&x.Issues[i])
			all = append(all, &x.Issues[i])
		}
		if err != nil {
			return all, err
		}
		if resp.NextPage < page {
			break
		}
		page = resp.NextPage
	}
	return all, nil
}

func queryToListOptions(q string) (opt github.IssueListByRepoOptions, ok bool) {
	if strings.ContainsAny(q, `"'`) {
		return
	}
	for _, f := range strings.Fields(q) {
		i := strings.Index(f, ":")
		if i < 0 {
			return
		}
		key, val := f[:i], f[i+1:]
		switch key {
		default:
			return
		case "state":
			if opt.State != "" || val == "" {
				return
			}
			opt.State = val
		case "assignee":
			if opt.Assignee != "" || val == "" {
				return
			}
			opt.Assignee = val
		case "author":
			if opt.Creator != "" || val == "" {
				return
			}
			opt.Creator = val
		case "mentions":
			if opt.Mentioned != "" || val == "" {
				return
			}
			opt.Mentioned = val
		case "label":
			if opt.Labels != nil || val == "" {
				return
			}
			opt.Labels = strings.Split(val, ",")
		case "sort":
			if opt.Sort != "" || val == "" {
				return
			}
			opt.Sort = val
		case "updated":
			if !opt.Since.IsZero() || !strings.HasPrefix(val, ">=") {
				return
			}
			// TODO: Can set Since if we parse val[2:].
			return
		case "no":
			switch val {
			default:
				return
			case "milestone":
				if opt.Milestone != "" {
					return
				}
				opt.Milestone = "none"
			}
		}
	}
	return opt, true
}

func listRepoIssues(opt github.IssueListByRepoOptions) ([]*github.Issue, error) {
	var all []*github.Issue
	for page := 1; ; {
		xopt := opt
		xopt.ListOptions = github.ListOptions{
			Page:    page,
			PerPage: 100,
		}
		issues, resp, err := client.Issues.ListByRepo(projectOwner, projectRepo, &xopt)
		for i := range issues {
			updateIssueCache(issues[i])
			all = append(all, issues[i])
		}
		if err != nil {
			return all, err
		}
		if resp.NextPage < page {
			break
		}
		page = resp.NextPage
	}

	// Filter out pull requests, since we cannot say type:issue like in searchIssues.
	// TODO(rsc): Rethink excluding pull requests.
	save := all[:0]
	for _, issue := range all {
		if issue.PullRequestLinks == nil {
			save = append(save, issue)
		}
	}
	return save, nil
}

func loadMilestones() ([]*github.Milestone, error) {
	// NOTE(rsc): There appears to be no paging possible.
	all, _, err := client.Issues.ListMilestones(projectOwner, projectRepo, &github.MilestoneListOptions{
		State: "open",
	})
	if err != nil {
		return nil, err
	}
	if all == nil {
		all = []*github.Milestone{}
	}
	return all, nil
}

func wrap(t string, prefix string) string {
	out := ""
	t = strings.Replace(t, "\r\n", "\n", -1)
	max := 70
	lines := strings.Split(t, "\n")
	for i, line := range lines {
		if i > 0 {
			out += "\n" + prefix
		}
		s := line
		for len(s) > max {
			i := strings.LastIndex(s[:max], " ")
			if i < 0 {
				i = max - 1
			}
			i++
			out += s[:i] + "\n" + prefix
			s = s[i:]
		}
		out += s
	}
	return out
}

var client *github.Client

// GitHub personal access token, from https://github.com/settings/applications.
var authToken string

func loadAuth() {
	const short = ".github-issue-token"
	filename := filepath.Clean(os.Getenv("HOME") + "/" + short)
	shortFilename := filepath.Clean("$HOME/" + short)
	if *tokenFile != "" {
		filename = *tokenFile
		shortFilename = *tokenFile
	}
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatal("reading token: ", err, "\n\n"+
			"Please create a personal access token at https://github.com/settings/tokens/new\n"+
			"and write it to ", shortFilename, " to use this program.\n"+
			"The token only needs the repo scope, or private_repo if you want to\n"+
			"view or edit issues for private repositories.\n"+
			"The benefit of using a personal access token over using your GitHub\n"+
			"password directly is that you can limit its use and revoke it at any time.\n\n")
	}
	fi, err := os.Stat(filename)
	if fi.Mode()&0077 != 0 {
		log.Fatalf("reading token: %s mode is %#o, want %#o", shortFilename, fi.Mode()&0777, fi.Mode()&0700)
	}
	authToken = strings.TrimSpace(string(data))
	t := &oauth2.Transport{
		Source: &tokenSource{AccessToken: authToken},
	}
	client = github.NewClient(&http.Client{Transport: t})
}

type tokenSource oauth2.Token

func (t *tokenSource) Token() (*oauth2.Token, error) {
	return (*oauth2.Token)(t), nil
}

func getInt(x *int) int {
	if x == nil {
		return 0
	}
	return *x
}

func getString(x *string) string {
	if x == nil {
		return ""
	}
	return *x
}

func getUserLogin(x *github.User) string {
	if x == nil || x.Login == nil {
		return ""
	}
	return *x.Login
}

func getTime(x *time.Time) time.Time {
	if x == nil {
		return time.Time{}
	}
	return (*x).Local()
}

func getMilestoneTitle(x *github.Milestone) string {
	if x == nil || x.Title == nil {
		return ""
	}
	return *x.Title
}

func getLabelNames(x []github.Label) []string {
	var out []string
	for _, lab := range x {
		out = append(out, getString(lab.Name))
	}
	sort.Strings(out)
	return out
}

var issueCache struct {
	sync.Mutex
	m map[int]*github.Issue
}

func updateIssueCache(issue *github.Issue) {
	n := getInt(issue.Number)
	if n == 0 {
		return
	}
	issueCache.Lock()
	if issueCache.m == nil {
		issueCache.m = make(map[int]*github.Issue)
	}
	issueCache.m[n] = issue
	issueCache.Unlock()
}

func bulkReadIssuesCached(ids []int) ([]*github.Issue, error) {
	var all []*github.Issue
	issueCache.Lock()
	for _, id := range ids {
		all = append(all, issueCache.m[id])
	}
	issueCache.Unlock()

	var errbuf bytes.Buffer
	for i, id := range ids {
		if all[i] == nil {
			issue, _, err := client.Issues.Get(projectOwner, projectRepo, id)
			if err != nil {
				fmt.Fprintf(&errbuf, "reading #%d: %v\n", id, err)
				continue
			}
			updateIssueCache(issue)
			all[i] = issue
		}
	}
	var err error
	if errbuf.Len() > 0 {
		err = fmt.Errorf("%s", strings.TrimSpace(errbuf.String()))
	}
	return all, err
}

// JSON output
// If you make changes to the structs, copy them back into the doc comment.

type Issue struct {
	Number    int
	Ref       string
	Title     string
	State     string
	Assignee  string
	Closed    time.Time
	Labels    []string
	Milestone string
	URL       string
	Reporter  string
	Created   time.Time
	Text      string
	Comments  []*Comment
}

type Comment struct {
	Author string
	Time   time.Time
	Text   string
}

func showJSONIssue(w io.Writer, issue *github.Issue) {
	data, err := json.MarshalIndent(toJSONWithComments(issue), "", "\t")
	if err != nil {
		log.Fatal(err)
	}
	data = append(data, '\n')
	w.Write(data)
}

func showJSONList(all []*github.Issue) {
	j := []*Issue{} // non-nil for json
	for _, issue := range all {
		j = append(j, toJSON(issue))
	}
	data, err := json.MarshalIndent(j, "", "\t")
	if err != nil {
		log.Fatal(err)
	}
	data = append(data, '\n')
	os.Stdout.Write(data)
}

func toJSON(issue *github.Issue) *Issue {
	j := &Issue{
		Number:    getInt(issue.Number),
		Ref:       fmt.Sprintf("%s/%s#%d\n", projectOwner, projectRepo, getInt(issue.Number)),
		Title:     getString(issue.Title),
		State:     getString(issue.State),
		Assignee:  getUserLogin(issue.Assignee),
		Closed:    getTime(issue.ClosedAt),
		Labels:    getLabelNames(issue.Labels),
		Milestone: getMilestoneTitle(issue.Milestone),
		URL:       fmt.Sprintf("https://github.com/%s/%s/issues/%d\n", projectOwner, projectRepo, getInt(issue.Number)),
		Reporter:  getUserLogin(issue.User),
		Created:   getTime(issue.CreatedAt),
		Text:      getString(issue.Body),
		Comments:  []*Comment{},
	}
	if j.Labels == nil {
		j.Labels = []string{}
	}
	return j
}

func toJSONWithComments(issue *github.Issue) *Issue {
	j := toJSON(issue)
	for page := 1; ; {
		list, resp, err := client.Issues.ListComments(projectOwner, projectRepo, getInt(issue.Number), &github.IssueListCommentsOptions{
			ListOptions: github.ListOptions{
				Page:    page,
				PerPage: 100,
			},
		})
		if err != nil {
			log.Fatal(err)
		}
		for _, com := range list {
			j.Comments = append(j.Comments, &Comment{
				Author: getUserLogin(com.User),
				Time:   getTime(com.CreatedAt),
				Text:   getString(com.Body),
			})
		}
		if resp.NextPage < page {
			break
		}
		page = resp.NextPage
	}
	return j
}
