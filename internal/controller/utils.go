package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	issuesv1 "dvir.io/githubissue/api/v1"
	"github.com/google/go-github/v56/github"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Checks if GithubIssue CRD has an issue in the repo
func searchForIssue(issue *issuesv1.GithubIssue, gitHubIssues []*github.Issue) *github.Issue {
	for _, ghIssue := range gitHubIssues {
		if strings.EqualFold(*ghIssue.Title, issue.Spec.Title) {

			return ghIssue
		}
	}
	return nil
}

// AddFinalizer adds finalizer to GithubIssue CRD
func (r *GithubIssueReconciler) AddFinalizer(ctx context.Context, issue *issuesv1.GithubIssue) (err error) {

	if !controllerutil.ContainsFinalizer(issue, CloseIssuesFinalizer) {
		r.Log.Info("adding finalizer")
		controllerutil.AddFinalizer(issue, CloseIssuesFinalizer)
		if err := r.Update(ctx, issue); err != nil {
			return fmt.Errorf("unable to add finalizer: %v", err.Error())
		}
		r.Log.Info("added finalizer")
		return nil
	} else {

		return nil
	}

}

// DeleteFinalizer deletes finalizer from GithubIssue CRD
func (r *GithubIssueReconciler) DeleteFinalizer(ctx context.Context, issue *issuesv1.GithubIssue) (bool, error) {
	if controllerutil.ContainsFinalizer(issue, CloseIssuesFinalizer) {
		r.Log.Info("removing finalizer")
		// remove finalizer
		controllerutil.RemoveFinalizer(issue, CloseIssuesFinalizer)
		if err := r.Update(ctx, issue); err != nil {
			return false, fmt.Errorf("failed removing finalizer: %v", err.Error())
		}
		r.Log.Info("Removed finalizer")
		return true, nil
	} else {
		//Finalizer already deleted
		return false, nil
	}
}

// UpdateIssueStatus updates the status of the GithubIssue CRD
func (r *GithubIssueReconciler) UpdateIssueStatus(ctx context.Context, issue *issuesv1.GithubIssue, githubIssue *github.Issue) error {
	PRChange := r.CheckForPr(githubIssue, issue)
	OpenChange := r.CheckIfOpen(githubIssue, issue)

	if OpenChange || PRChange {
		r.Log.Info("editing Issue status")
		err := r.Client.Status().Update(ctx, issue)
		if err != nil {
			//Necessary for tests
			if err := r.Client.Update(ctx, issue); err != nil {
				return fmt.Errorf("unable to update status of CR: %v", err.Error())
			}
		}
		r.Log.Info("updated Issue status")
		return nil

	}
	return nil

}

// CheckIfOpen check if issue is open
func (r *GithubIssueReconciler) CheckIfOpen(githubIssue *github.Issue, issueObject *issuesv1.GithubIssue) bool {
	condition := &v1.Condition{Type: "IssueIsOpen", Status: v1.ConditionTrue, Reason: "IssueIsOpen", Message: "Issue is open"}
	if state := githubIssue.GetState(); state != "open" {
		condition = &v1.Condition{Type: "IssueIsOpen", Status: v1.ConditionFalse, Reason: fmt.Sprintf("Issueis%s", state), Message: fmt.Sprintf("Issue is %s", state)}
	}
	if !meta.IsStatusConditionPresentAndEqual(issueObject.Status.Conditions, "IssueIsOpen", condition.Status) {
		meta.SetStatusCondition(&issueObject.Status.Conditions, *condition)
		return true
	}
	return false
}

// CheckForPr check if issue has an open PR
func (r *GithubIssueReconciler) CheckForPr(githubIssue *github.Issue, issueObject *issuesv1.GithubIssue) bool {
	condition := &v1.Condition{Type: "IssueHasPR", Status: v1.ConditionFalse, Reason: "IssueHasnopr", Message: "Issue has no pr"}
	if githubIssue.GetPullRequestLinks() != nil {
		condition = &v1.Condition{Type: "IssueHasPR", Status: v1.ConditionTrue, Reason: "IssueHasPR", Message: "Issue Has an open PR"}
	}
	if !meta.IsStatusConditionPresentAndEqual(issueObject.Status.Conditions, "IssueHasPR", condition.Status) {
		meta.SetStatusCondition(&issueObject.Status.Conditions, *condition)
		return true
	}
	return false
}

// fetchAllIssues gets all issues in repo
func (r *GithubIssueReconciler) fetchAllIssues(ctx context.Context, owner string, repo string) ([]*github.Issue, error) {
	opt := &github.IssueListByRepoOptions{}
	allIssues, response, err := r.GitHubClient.Issues.ListByRepo(ctx, owner, repo, opt)
	if err != nil {
		if response != nil {
			return []*github.Issue{}, fmt.Errorf("got bad response from GitHub: %s: %v", response.Status, err.Error())
		}
		return []*github.Issue{}, fmt.Errorf("failed fetching issues: %v", err.Error())
	}
	r.Log.Info("fetched issues")
	return allIssues, nil
}

// CloseIssue closes the issue on GitHub
func (r *GithubIssueReconciler) CloseIssue(ctx context.Context, owner string, repo string, gitHubIssue *github.Issue) error {
	if gitHubIssue == nil {
		err := errors.New("could not find issue in repo")

		return err
	}
	state := "closed"
	closedIssueRequest := &github.IssueRequest{State: &state}
	_, _, err := r.GitHubClient.Issues.Edit(ctx, owner, repo, *gitHubIssue.Number, closedIssueRequest)
	if err != nil {
		err := errors.New("could not close issue")
		return err
	}
	return nil
}

// CreateIssue add an issue to the repo
func (r *GithubIssueReconciler) CreateIssue(ctx context.Context, owner string, repo string, issueObject *issuesv1.GithubIssue) error {
	newIssue := &github.IssueRequest{Title: &issueObject.Spec.Title, Body: &issueObject.Spec.Description}
	_, response, err := r.GitHubClient.Issues.Create(ctx, owner, repo, newIssue)
	if err != nil {
		if response != nil {
			return fmt.Errorf("failed creating issue: status %s: %v", response.Status, err.Error())
		} else {
			return fmt.Errorf("failed creating error: %v", err.Error())

		}
	}
	if response.StatusCode != 201 {
		return fmt.Errorf("failed creating issue: status %s", response.Status)
	}
	return nil
}

// EditIssue change the description of an existing issue in the repo
func (r *GithubIssueReconciler) EditIssue(ctx context.Context, owner string, repo string, issueObject *issuesv1.GithubIssue, issueNumber int) error {
	editIssueRequest := &github.IssueRequest{Body: &issueObject.Spec.Description}
	_, response, err := r.GitHubClient.Issues.Edit(ctx, owner, repo, issueNumber, editIssueRequest)
	if err != nil {
		if response != nil {

			return fmt.Errorf("failed editing issue: %v", err.Error())
		}
		return fmt.Errorf("failed editing issue: status %s: %v", response.Status, err.Error())

	}
	return nil
}

func (r *GithubIssueReconciler) FindIssue(ctx context.Context, owner string, repo string, issue *issuesv1.GithubIssue) (*github.Issue, error) {
	allIssues, err := r.fetchAllIssues(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("falied fetching error: %v", err.Error())
	}
	return searchForIssue(issue, allIssues), nil
}
