// Copyright 2024 Daytona Platforms Inc.
// SPDX-License-Identifier: Apache-2.0

package gitprovider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"net/http"
	"net/url"

	bitbucketv1 "github.com/gfleury/go-bitbucket-v1"
	"github.com/mitchellh/mapstructure"
)

type BitbucketServerGitProvider struct {
	*AbstractGitProvider

	username   string
	token      string
	baseApiUrl string
}

func NewBitbucketServerGitProvider(username string, token string, baseApiUrl string) *BitbucketServerGitProvider {
	provider := &BitbucketServerGitProvider{
		username:            username,
		token:               token,
		AbstractGitProvider: &AbstractGitProvider{},
		baseApiUrl:          baseApiUrl,
	}
	provider.AbstractGitProvider.GitProvider = provider

	return provider
}

func (g *BitbucketServerGitProvider) CanHandle(repoUrl string) (bool, error) {
	staticContext, err := g.ParseStaticGitContext(repoUrl)
	if err != nil {
		return false, err
	}

	return strings.Contains(g.baseApiUrl, staticContext.Source), nil
}

func (g *BitbucketServerGitProvider) getApiClient() (*bitbucketv1.APIClient, error) {
	conf := bitbucketv1.NewConfiguration(g.baseApiUrl)
	ctx := context.WithValue(context.Background(), bitbucketv1.ContextBasicAuth, bitbucketv1.BasicAuth{
		UserName: g.username,
		Password: g.token,
	})
	client := bitbucketv1.NewAPIClient(ctx, conf)
	return client, nil
}

func (g *BitbucketServerGitProvider) GetNamespaces(options ListOptions) ([]*GitNamespace, error) {
	client, err := g.getApiClient()
	if err != nil {
		return nil, err
	}

	var namespaces []*GitNamespace

	projectsRaw, err := client.DefaultApi.GetProjects(map[string]any{
		"start": options.Page,
		"limit": options.PerPage,
	})
	if err != nil {
		return nil, g.FormatError(projectsRaw.StatusCode, projectsRaw.Message)
	}

	projectsRaw.Body.Close()

	projects, err := bitbucketv1.GetProjectsResponse(projectsRaw)
	if err != nil {
		return nil, err
	}

	for _, project := range projects {
		namespace := &GitNamespace{}
		namespace.Id = project.Key
		namespace.Name = project.Name

		namespaces = append(namespaces, namespace)
	}

	return namespaces, nil
}

func (g *BitbucketServerGitProvider) GetRepositories(namespace string, options ListOptions) ([]*GitRepository, error) {
	client, err := g.getApiClient()
	if err != nil {
		return nil, err
	}

	var response []*GitRepository
	var repoList *bitbucketv1.APIResponse

	opts := map[string]interface{}{
		"start": options.Page,
		"limit": options.PerPage,
	}
	if namespace == personalNamespaceId {
		repoList, err = client.DefaultApi.GetRepositories_19(opts)
	} else {
		repoList, err = client.DefaultApi.GetRepositoriesWithOptions(namespace, opts)
	}

	if err != nil {
		return nil, err
	}

	pageRepos, err := bitbucketv1.GetRepositoriesResponse(repoList)
	if err != nil {
		return nil, err
	}

	for _, repo := range pageRepos {
		var repoUrl string
		for _, link := range repo.Links.Clone {
			if link.Name == "https" || link.Name == "http" {
				repoUrl = link.Href
				break
			}
		}

		if len(repoUrl) == 0 && repo.Links != nil {
			repoUrl = repo.Links.Self[0].Href
		}

		var ownerName string
		if repo.Owner != nil {
			ownerName = repo.Owner.Name
		}

		baseURL, err := url.Parse(g.baseApiUrl)
		if err != nil {
			return nil, g.FormatError(repoList.StatusCode, repoList.Message)
		}

		response = append(response, &GitRepository{
			Id:     repo.Slug,
			Name:   repo.Name,
			Url:    repoUrl,
			Source: baseURL.Host,
			Owner:  ownerName,
		})
	}

	return response, nil
}

func (g *BitbucketServerGitProvider) GetRepoBranches(repositoryId string, namespaceId string, options ListOptions) ([]*GitBranch, error) {
	client, err := g.getApiClient()
	if err != nil {
		return nil, err
	}

	var response []*GitBranch

	branches, err := client.DefaultApi.GetBranches(namespaceId, repositoryId, map[string]interface{}{
		"start": options.Page,
		"limit": options.PerPage,
	})
	if err != nil {
		return nil, g.FormatError(branches.StatusCode, branches.Message)
	}

	branchList, err := bitbucketv1.GetBranchesResponse(branches)
	if err != nil {
		return nil, err
	}

	for _, branch := range branchList {
		response = append(response, &GitBranch{
			Name: branch.DisplayID,
			Sha:  branch.LatestCommit,
		})
	}

	return response, nil
}

func (g *BitbucketServerGitProvider) GetRepoPRs(repositoryId string, namespaceId string, options ListOptions) ([]*GitPullRequest, error) {
	client, err := g.getApiClient()
	if err != nil {
		return nil, err
	}

	var response []*GitPullRequest

	prList, err := client.DefaultApi.GetPullRequests(map[string]interface{}{
		"start": options.Page,
		"limit": options.PerPage,
	})
	if err != nil {
		return nil, g.FormatError(prList.StatusCode, prList.Message)
	}

	pullRequest, err := bitbucketv1.GetPullRequestsResponse(prList)
	if err != nil {
		return nil, err
	}

	for _, pr := range pullRequest {
		var repoUrl string
		for _, link := range pr.FromRef.Repository.Links.Clone {
			if link.Name == "https" || link.Name == "http" {
				repoUrl = link.Href
				break
			}
		}

		if len(repoUrl) == 0 && pr.FromRef.Repository.Links != nil {
			repoUrl = pr.FromRef.Repository.Links.Self[0].Href
		}

		var repoOwner string
		if pr.FromRef.Repository.Owner != nil {
			repoOwner = pr.FromRef.Repository.Owner.Name
		}
		response = append(response, &GitPullRequest{
			Name:            pr.Title,
			Branch:          pr.FromRef.DisplayID,
			Sha:             pr.FromRef.LatestCommit,
			SourceRepoId:    pr.FromRef.Repository.Slug,
			SourceRepoUrl:   repoUrl,
			SourceRepoOwner: repoOwner,
			SourceRepoName:  pr.FromRef.Repository.Name,
		})
	}

	return response, nil
}

func (g *BitbucketServerGitProvider) GetUser() (*GitUser, error) {
	client, err := g.getApiClient()
	if err != nil {
		return nil, err
	}

	// Since BitbucketServer or gfleury/go-bitbucket-v1 doesn't offer an endpoint to query the
	// currently authenticated user, we instead query the '/rest/api/1.0/application-properties' endpoint
	// which does not put load on the server and then extract the username from the response header.
	// Refer to this developer community comment: https://community.developer.atlassian.com/t/obtain-authorised-users-username-from-api/24422/2
	res, err := client.DefaultApi.GetApplicationProperties()
	if err != nil {
		return nil, g.FormatError(res.StatusCode, res.Message)
	}

	username := res.Header.Get("X-Ausername")
	if username == "" {
		return nil, errors.New("X-Ausername header is missing")
	}

	user, err := client.DefaultApi.GetUser(username)
	if err != nil {
		return nil, err
	}

	if user.Values == nil {
		return nil, errors.New("user values are nil")
	}
	var userInfo bitbucketv1.User
	err = mapstructure.Decode(user.Values, &userInfo)

	if err != nil {
		return nil, err
	}

	response := &GitUser{}
	response.Id = fmt.Sprintf("%d", userInfo.ID)
	response.Username = username
	response.Name = userInfo.DisplayName

	return response, nil
}

func (g *BitbucketServerGitProvider) GetLastCommitSha(staticContext *StaticGitContext) (string, error) {
	client, err := g.getApiClient()
	if err != nil {
		return "", err
	}

	until := ""
	if staticContext.Sha != nil {
		until = *staticContext.Sha
	}

	commits, err := client.DefaultApi.GetCommits(staticContext.Id, staticContext.Name, map[string]interface{}{
		"until": until,
	})

	if err != nil {
		return "", g.FormatError(commits.StatusCode, commits.Message)
	}

	if len(commits.Values) == 0 {
		return "", errors.New("no commits found")
	}

	commitList, err := bitbucketv1.GetCommitsResponse(commits)
	if err != nil {
		return "", err
	}

	return commitList[0].ID, nil
}

func (g *BitbucketServerGitProvider) GetBranchByCommit(staticContext *StaticGitContext) (string, error) {
	client, err := g.getApiClient()
	if err != nil {
		return "", err
	}

	branches, err := client.DefaultApi.GetBranches(staticContext.Owner, staticContext.Name, map[string]interface{}{})
	if err != nil {
		return "", g.FormatError(branches.StatusCode, branches.Message)
	}

	branchList, err := bitbucketv1.GetBranchesResponse(branches)
	if err != nil {
		return "", err
	}

	var branchName string
	for _, branch := range branchList {
		if branch.LatestCommit == *staticContext.Sha {
			branchName = branch.DisplayID
			break
		}

		commits, err := client.DefaultApi.GetCommits(staticContext.Owner, staticContext.Name, map[string]interface{}{
			"since": *staticContext.Sha,
			"until": branch.LatestCommit,
		})
		if err != nil {
			return "", g.FormatError(commits.StatusCode, commits.Message)
		}

		if len(commits.Values) == 0 {
			continue
		}

		commitList, err := bitbucketv1.GetCommitsResponse(commits)
		if err != nil {
			return "", err
		}

		for _, commit := range commitList {
			if *staticContext.Sha == commit.ID {
				branchName = branch.DisplayID
				break
			}
		}

		if branchName != "" {
			break
		}
	}

	if branchName == "" {
		return "", fmt.Errorf("status code: %d branch not found for SHA: %s", http.StatusNotFound, *staticContext.Sha)
	}

	return branchName, nil
}

func (g *BitbucketServerGitProvider) GetUrlFromContext(repoContext *GetRepositoryContext) string {
	url := strings.TrimSuffix(repoContext.Url, ".git")
	url = strings.Replace(url, "/scm/", "/", 1)

	if repoContext.Branch != nil && *repoContext.Branch != "" {
		url += "/src/" + *repoContext.Branch

		if repoContext.Path != nil && *repoContext.Path != "" {
			url += "/" + *repoContext.Path
		}
	} else if repoContext.Path != nil && *repoContext.Path != "" {
		url += "/src/main/" + *repoContext.Path
	}

	return url
}

func (g *BitbucketServerGitProvider) GetPrContext(staticContext *StaticGitContext) (*StaticGitContext, error) {
	if staticContext.PrNumber == nil {
		return staticContext, nil
	}

	repo := *staticContext

	client, err := g.getApiClient()
	if err != nil {
		return nil, err
	}

	pr, err := client.DefaultApi.GetPullRequest(staticContext.Id, staticContext.Name, int(*staticContext.PrNumber))
	if err != nil {
		return nil, g.FormatError(pr.StatusCode, pr.Message)
	}

	prInfo, err := bitbucketv1.GetPullRequestResponse(pr)
	if err != nil {
		return nil, err
	}

	if prInfo.FromRef.Repository.Owner != nil {
		ownerName := prInfo.FromRef.Repository.Owner.DisplayName
		repo.Owner = ownerName
		repo.Id = ownerName
	}
	repo.Name = prInfo.FromRef.Repository.Slug
	repo.Branch = &prInfo.FromRef.DisplayID

	return &repo, nil
}

func (g *BitbucketServerGitProvider) ParseStaticGitContext(repoUrl string) (*StaticGitContext, error) {
	var staticContext StaticGitContext

	// optional string - '/rest/api/'
	re := regexp.MustCompile(`(https?://[^/]+)(?:/rest/api/[^/]+)?/projects/([^/]+)/repos/([^/]+)(?:/([^/?#]+))?(?:/([^/?#\\]+))?(?:\?at=refs%2Fheads%2F([^/?#]+))?`)
	matches := re.FindStringSubmatch(repoUrl)

	if len(matches) < 4 {
		// // Handle scm format
		re = regexp.MustCompile(`(https?://[^/]+)/scm/([^/]+)/([^/.]+)(?:\.git)?(?:/([^/?#]+))?(?:/([^/?#\\]+))?(?:\?at=refs%2Fheads%2F([^/?#]+))?`)
		matches = re.FindStringSubmatch(repoUrl)
		if len(matches) < 4 {
			return nil, fmt.Errorf("could not extract project key and repo name from URL: %s", repoUrl)
		}
	}

	baseUrl := matches[1]
	projectKey := matches[2]
	repoName := matches[3]
	action := matches[4]
	// action is either 'pull-requests', 'browse', 'commits'
	// identifier is either pull request number or path or commit SHA
	identifier := matches[5]
	branchName := matches[6]

	staticContext.Id = projectKey
	staticContext.Name = repoName
	staticContext.Owner = projectKey
	// For '.git' or repo clone over https format, refer to https://community.atlassian.com/t5/Bitbucket-questions/Project-key-in-repositories-URL/qaq-p/578207
	// and https://community.atlassian.com/t5/Bitbucket-questions/remote-url-in-Bitbucket-server-what-does-scm-represent-is-it/qaq-p/2060987
	staticContext.Url = fmt.Sprintf("%s/scm/%s/%s.git", baseUrl, projectKey, repoName)
	staticContext.Source = strings.TrimPrefix(baseUrl, "https://")

	switch action {
	case "pull-requests":
		if prNumber, err := strconv.Atoi(identifier); err == nil {
			prUint := uint32(prNumber)
			staticContext.PrNumber = &prUint
		}
	case "browse":
		if branchName != "" {
			staticContext.Branch = &branchName
		} else if strings.Contains(repoUrl, "browse/") {
			if identifier != "" {
				staticContext.Path = &identifier
			}
		} else if strings.Contains(repoUrl, "browse?") {
			if strings.Contains(repoUrl, "at=refs%2Fheads%2F") {
				parts := strings.Split(repoUrl, "at=refs%2Fheads%2F")
				if len(parts) == 2 {
					branchName = parts[1]
					staticContext.Branch = &branchName
				}
			}
		}
	case "commits":
		if identifier != "" {
			staticContext.Sha = &identifier
			staticContext.Branch = &identifier
		} else if strings.Contains(repoUrl, "commits?until=") {
			parts := strings.Split(repoUrl, "commits?until=")
			if len(parts) == 2 {
				sha := parts[1]
				staticContext.Sha = &sha
				staticContext.Branch = &sha
			}
		}
	}

	return &staticContext, nil
}

func (g *BitbucketServerGitProvider) GetDefaultBranch(staticContext *StaticGitContext) (*string, error) {
	client, err := g.getApiClient()
	if err != nil {
		return nil, err
	}

	branches, err := client.DefaultApi.GetBranches(staticContext.Id, staticContext.Name, nil)
	if err != nil {
		return nil, g.FormatError(branches.StatusCode, branches.Message)
	}

	branchList, err := bitbucketv1.GetBranchesResponse(branches)
	if err != nil {
		return nil, err
	}

	for _, branch := range branchList {
		if branch.IsDefault {
			return &branch.DisplayID, nil
		}
	}

	return nil, errors.New("default branch not found")
}

func (b *BitbucketServerGitProvider) FormatError(statusCode int, message string) error {

	return fmt.Errorf("status code: %d err: Request failed with %s", statusCode, message)
}
