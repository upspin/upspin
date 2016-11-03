// Copyright 2015 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/go-github/github"
)

const timeFormat = "2006-01-02 15:04:05"

func printIssue(w io.Writer, issue *github.Issue) error {
	fmt.Fprintf(w, "Title: %s\n", getString(issue.Title))
	fmt.Fprintf(w, "State: %s\n", getString(issue.State))
	fmt.Fprintf(w, "Assignee: %s\n", getUserLogin(issue.Assignee))
	/*
		if issue.ClosedAt != nil {
			fmt.Fprintf(w, "Closed: %s\n", getTime(issue.ClosedAt).Format(timeFormat))
		}
		fmt.Fprintf(w, "Labels: %s\n", strings.Join(getLabelNames(issue.Labels), " "))
		fmt.Fprintf(w, "Milestone: %s\n", getMilestoneTitle(issue.Milestone))
		fmt.Fprintf(w, "URL: https://github.com/%s/%s/issues/%d\n", projectOwner, projectRepo, getInt(issue.Number))

		fmt.Fprintf(w, "\nReported by %s (%s)\n", getUserLogin(issue.User), getTime(issue.CreatedAt).Format(timeFormat))
		if issue.Body != nil {
			text := strings.TrimSpace(*issue.Body)
			if text != "" {
				fmt.Fprintf(w, "\n\t%s\n", wrap(text, "\t"))
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
					text := strings.TrimSpace(*com.Body)
					if text != "" {
						fmt.Fprintf(w, "\n\t%s\n", wrap(text, "\t"))
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

	*/
	return nil
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
