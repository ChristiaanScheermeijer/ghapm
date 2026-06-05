package cmd

import (
	"os"

	githubclient "github.com/christiaanscheermeijer/ghapm/internal/githubclient"
)

func newGitHubClient(useAPI bool) githubclient.Client {
	if useAPI {
		return githubclient.NewCachingClient(githubclient.NewRESTClient(os.Getenv("GITHUB_TOKEN")))
	}
	return githubclient.NewCachingClient(githubclient.NewCLIClient())
}
