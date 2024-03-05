package main

import (
	"fmt"
	"log"
	"os"
	"context"
	"time"
	"sort"
	"bytes"
	"encoding/json"

	"net/http"
	"encoding/base64"

	"github.com/joho/godotenv"

	"golang.org/x/oauth2"
	graphql "github.com/hasura/go-graphql-client"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

var client *graphql.Client

func getNameById(login string)string {
	var query struct {
		User struct {
			Name string
		} `graphql:"user(login: $login)"`
	}

	variables := map[string]interface{} {
		"login": login,
	}

	if err := client.Query(context.Background(), &query, variables); err != nil {
		log.Fatal(err)
	}

	return query.User.Name
}

func printMetricsForGithub(initialDate, endDate time.Time) {
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		fmt.Println("GITHUB_TOKEN not provided. Skipping this report.")
		return
	}

	githubOwner := os.Getenv("GITHUB_OWNER")
	if githubOwner == "" {
		fmt.Println("GITHUB_OWNER not provided. Skipping this report.")
		return
	}

	githubRepo := os.Getenv("GITHUB_REPO")
	if githubOwner == "" {
		fmt.Println("GITHUB_REPO not provided. Skipping this report.")
		return
	}

	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: githubToken})
	httpClient := oauth2.NewClient(context.Background(), src)

	client = graphql.NewClient("https://api.github.com/graphql", httpClient)

	type pullRequest struct {
		Author struct {
			Login string
		}
		Title string
		CreatedAt time.Time
		Additions int
		Deletions int
		ChangedFiles int
		TotalCommentsCount int
		Closed bool
		ClosedAt time.Time
		Merged bool
		MergedAt time.Time
	}

	var query struct {
		Repository struct {
			PullRequest struct {
				Nodes []pullRequest

				PageInfo struct {
					HasNextPage bool
					EndCursor string
				}
			} `graphql:"pullRequests(first: 100, orderBy: {direction: DESC, field: CREATED_AT}, after: $prCursor)"`
		} `graphql:"repository(owner: $owner, name: $repo)"`
	}

	variables := map[string]interface{}{
		"owner":	githubOwner,
		"repo":		githubRepo,
		"prCursor":	(*string)(nil),
	}

	var allPRs []pullRequest
	out:
	for {
		if ptr, ok := variables["prCursor"].(*string); ok && ptr == nil {
			fmt.Println("Requesting first page")
		} else {
			fmt.Printf("Requesting page with node: %s\n", *ptr)
		}

		// This is very stupid, but we need to reset the slice before each iteration
		query.Repository.PullRequest.Nodes = nil
		if err := client.Query(context.Background(), &query, variables); err != nil {
			log.Fatalf("Error in GraphQL query: %v", err)
		}

		if len(query.Repository.PullRequest.Nodes) == 0 {
			break
		}

		for _, pr := range query.Repository.PullRequest.Nodes {
			if pr.CreatedAt.After(endDate) {
				continue
			}

			if pr.CreatedAt.After(initialDate) {
				allPRs = append(allPRs, pr)
			} else {
				break out
			}
		}

		if !query.Repository.PullRequest.PageInfo.HasNextPage {
			break
		}

		variables["prCursor"] = &query.Repository.PullRequest.PageInfo.EndCursor
	}

	fmt.Printf("%d PRs were created between %v - %v\n", len(allPRs), initialDate, endDate)

	var prByUser map[string][]pullRequest = make(map[string][]pullRequest)

	for _, pr := range allPRs {
		prByUser[pr.Author.Login] = append(prByUser[pr.Author.Login], pr)
	}

	var sortedLogins []string
	for login, _ := range prByUser {
		sortedLogins = append(sortedLogins, login)
	}
	sort.Strings(sortedLogins)

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"ID", "Name", "Total PRs", "Merged PRs", "Merged PRs (%)", "Open PRs", "Added lines" , "Removed lines", "Changed files"})

	fmt.Print("Parsing data ")
	totalPRs 			:= 0
	totalMergedPRs		:= 0
	totalAddedLines		:= 0
	totalRemovedLines	:= 0
	totalChangedFiles	:= 0
	for _, login := range sortedLogins {
		fmt.Print(".")

		name := getNameById(login)

		addedLines 		:= 0
		removedLines 	:= 0
		changedFiles 	:= 0
		mergedPRs 		:= 0
		openPRs			:= 0
		for _, pr := range prByUser[login] {
			addedLines 		+= pr.Additions
			removedLines 	+= pr.Deletions
			changedFiles 	+= pr.ChangedFiles

			if pr.Merged && !pr.MergedAt.After(endDate) {
				mergedPRs++
			} else if !pr.Closed || pr.ClosedAt.After(endDate) {
				openPRs++
			}
		}

		numPRs := len(prByUser[login])
		t.AppendRow([]interface{}{
			login,
			name,
			numPRs,
			mergedPRs,
			fmt.Sprintf("%.1f%%", float64(mergedPRs*100)/float64(numPRs)),
			openPRs,
			addedLines,
			removedLines,
			changedFiles,
		})
		t.AppendSeparator()

		totalPRs 			+= numPRs
		totalMergedPRs		+= mergedPRs
		totalAddedLines		+= addedLines
		totalRemovedLines	+= removedLines
		totalChangedFiles	+= changedFiles
	}

	fmt.Println()
	fmt.Println()

	t.AppendFooter(table.Row{
		"Averages",
		"",
		fmt.Sprintf("%.1f", float64(totalPRs)/float64(len(sortedLogins))),
		fmt.Sprintf("%.1f", float64(totalMergedPRs)/float64(len(sortedLogins))),
		"",
		"",
		fmt.Sprintf("%.1f", float64(totalAddedLines)/float64(len(sortedLogins))),
		fmt.Sprintf("%.1f", float64(totalRemovedLines)/float64(len(sortedLogins))),
		fmt.Sprintf("%.1f", float64(totalChangedFiles)/float64(len(sortedLogins))),
	})

    t.SetColumnConfigs([]table.ColumnConfig{
        {Number: 3, Align: text.AlignCenter, AlignFooter: text.AlignCenter},
        {Number: 4, Align: text.AlignCenter, AlignFooter: text.AlignCenter},
        {Number: 5, Align: text.AlignCenter, AlignFooter: text.AlignCenter},
        {Number: 6, Align: text.AlignCenter, AlignFooter: text.AlignCenter},
        {Number: 7, Align: text.AlignCenter, AlignFooter: text.AlignCenter},
        {Number: 8, Align: text.AlignCenter, AlignFooter: text.AlignCenter},
        {Number: 9, Align: text.AlignCenter, AlignFooter: text.AlignCenter},
    })
	t.Render()
}

func printMetricsForJira(initialDate, endDate time.Time) {
	jiraBaseUrl := os.Getenv("JIRA_BASE_URL")
	if jiraBaseUrl == "" {
		fmt.Println("JIRA_BASE_URL not provided. Skipping this report.")
		return
	}

	jiraUser := os.Getenv("JIRA_USER")
	if jiraUser == "" {
		fmt.Println("JIRA_USER not provided. Skipping this report.")
		return
	}

	jiraToken := os.Getenv("JIRA_TOKEN")
	if jiraToken == "" {
		fmt.Println("JIRA_TOKEN not provided. Skipping this report.")
		return
	}

	jiraProject := os.Getenv("JIRA_PROJECT")
	if jiraProject == "" {
		fmt.Println("JIRA_PROJECT not provided. Skipping this report.")
		return
	}

	type jiraReport struct {
		Total int
		Issues []struct {
			Key string
			Fields struct {
				Summary string
				Assignee struct {
					DisplayName string
				}
				IssueType struct {
					Name string
				}
			}
			Changelog struct {
				Histories []struct {
					Author struct {
						DisplayName string
					}
					Items []struct {
						Field string
						ToString string
					}
				}
			}
		}
	}

	client := &http.Client{}

	totalIssues := 0
	countByPerson := make(map[string]struct{
		totalInProgress int
		spikeInProgress int
	})

	payload := `{
		"fields": ["summary", "assignee", "issuetype"],
		"expand": ["changelog"],
		"jql": "project = \"%s\" and status changed DURING (%s, %s) TO \"In Progress\" and issuetype not in (Epic, sub-task) ORDER BY assignee ASC",
		"startAt": %d
	}`
	offset := 0

	for {
		body := []byte(fmt.Sprintf(payload, jiraProject, initialDate.Format("2006-01-02"), endDate.Format("2006-01-02"), offset))

		req, err := http.NewRequest("POST", jiraBaseUrl + "/rest/api/2/search", bytes.NewBuffer(body))
		if err != nil {
			log.Fatal(err)
		}

		auth := jiraUser + ":" + jiraToken
		req.Header.Add("Authorization", "Basic " + base64.StdEncoding.EncodeToString([]byte(auth)))
		req.Header.Add("Accept", "application/json")
		req.Header.Add("Content-Type", "application/json")

		fmt.Println("Requesting the 50 items to JIRA")

		res, err := client.Do(req)
		if err != nil {
			log.Fatal(err)
		}

		defer res.Body.Close()

		report := &jiraReport{}
		err = json.NewDecoder(res.Body).Decode(report)
		if err != nil {
			log.Fatal(err)
		}

		totalIssues += report.Total
		for _, issue := range report.Issues {
			next:
			for i:=len(issue.Changelog.Histories)-1; i>=0; i-- {
				for _, item := range issue.Changelog.Histories[i].Items {
					if item.Field == "status" && item.ToString == "In Progress" {
						person := countByPerson[issue.Changelog.Histories[i].Author.DisplayName]
						person.totalInProgress++

						if issue.Fields.IssueType.Name == "Spike" {
							person.spikeInProgress++
						}

						countByPerson[issue.Changelog.Histories[i].Author.DisplayName] = person
						break next
					}
				}
			}
		}

		if report.Total < 50 {
			break
		}
		offset += 50
	}

	fmt.Printf("%d tickets were moved into progress between %v - %v\n", totalIssues, initialDate, endDate)

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"Name", "Total In Progress", "Spike In Progress"})

	for person, count := range countByPerson {
		t.AppendRow([]interface{}{
			person,
			count.totalInProgress,
			count.spikeInProgress,
		})
		t.AppendSeparator()
	}
    t.SetColumnConfigs([]table.ColumnConfig{
        {Number: 2, Align: text.AlignCenter, AlignFooter: text.AlignCenter},
        {Number: 3, Align: text.AlignCenter, AlignFooter: text.AlignCenter},
    })
	t.Render()
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	if len(os.Args) < 2 {
		log.Fatal("pull-metrics <start date> [<end date>]. E.g.: pull-metrics 2024-02-28 [2024-03-15]")
	}

	initialDate, err := time.Parse("2006-1-2", os.Args[1])
	if err != nil {
		log.Fatalf("Error parsing the time: %v", err)
	}

	endDate := time.Now()
	if len(os.Args) > 2 {
		if date, err := time.Parse("2006-1-2", os.Args[2]); err == nil {
			endDate = date.Add(time.Hour * 24 - time.Second)
		}
	}

	printMetricsForGithub(initialDate, endDate)

	fmt.Println()

	printMetricsForJira(initialDate, endDate)
}
