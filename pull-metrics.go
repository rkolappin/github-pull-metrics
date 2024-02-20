package main

import (
	"fmt"
	"log"
	"os"
	"context"
	"time"
	"sort"

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

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	if len(os.Args) < 2 {
		log.Fatal("pull-metrics <start date> [<end date>]. E.g.: pull-metrics 2024-02-28 [2024-03-15]")
	}

	initialDate, err := time.Parse("2006-01-02", os.Args[1])
	if err != nil {
		log.Fatalf("Error parsing the time: %v", err)
	}

	endDate := time.Now()
	if len(os.Args) > 2 {
		if date, err := time.Parse("2006-01-02", os.Args[2]); err == nil {
			endDate = date.Add(time.Hour * 24 - time.Second)
		}
	}

	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		log.Fatal("GITHUB_TOKEN environment variable needed")
	}

	githubOwner := os.Getenv("GITHUB_OWNER")
	if githubOwner == "" {
		log.Fatal("GITHUB_OWNER environment variable needed")
	}

	githubRepo := os.Getenv("GITHUB_REPO")
	if githubOwner == "" {
		log.Fatal("GITHUB_OWNER environment variable needed")
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
		Merged bool
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

			if pr.Merged {
				mergedPRs++
			} else if !pr.Closed {
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
