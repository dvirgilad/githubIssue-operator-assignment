package controller

import (
	"context"
	issuesv1 "dvir.io/githubissue/api/v1"
	"github.com/google/go-github/v58/github"
	giturl "github.com/kubescape/go-git-url"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strings"
)

func isGitHubURL(inputUrl string) (giturl.IGitURL, error) {
	url, err := giturl.NewGitURL(inputUrl)
	if err != nil {
		return nil, errors.New("invalid url")
	}
	return url, nil
}
func searchForIssue(issue *issuesv1.GithubIssue, gitHubIssues []*github.Issue) *github.Issue {
	for _, ghIssue := range gitHubIssues {
		if strings.ToUpper(*ghIssue.Title) == strings.ToUpper(issue.Spec.Title) {
			return ghIssue
		}
	}
	return nil
}

func searchForPR(timeline []*github.Timeline) v1.ConditionStatus {
	for i := len(timeline) - 1; i >= 0; i-- {
		event := timeline[i]
		action := event.Event
		if *action == "connected" || *action == "cross-referenced" {
			return v1.ConditionTrue
		}
		if *action == "disconnected" {
			return v1.ConditionFalse
		}
	}
	return v1.ConditionFalse

}
func (r *GithubIssueReconciler) AddFinalizer(ctx context.Context, issue *issuesv1.GithubIssue) (ok bool, err error) {

	if !controllerutil.ContainsFinalizer(issue, CloseIssuesFinalizer) {
		r.Log.Info("adding finalizer")
		controllerutil.AddFinalizer(issue, CloseIssuesFinalizer)
		if err := r.Update(ctx, issue); err != nil {
			r.Log.Error("unable to add finalizer", zap.Error(err))
			return false, err
		}
		r.Log.Info("added finalizer")
		return true, nil
	} else {

		return false, nil
	}

}

func (r *GithubIssueReconciler) DeleteFinalizer(ctx context.Context, issue *issuesv1.GithubIssue) (bool, error) {
	if controllerutil.ContainsFinalizer(issue, CloseIssuesFinalizer) {
		r.Log.Info("removing finalizer")
		// remove finalizer
		controllerutil.RemoveFinalizer(issue, CloseIssuesFinalizer)
		if err := r.Update(ctx, issue); err != nil {
			r.Log.Error("error removing finalizer", zap.Error(err))
			return false, err
		}
		r.Log.Info("Removed finalizer")
		return true, nil
	} else {
		//Finalizer already deleted
		return false, nil
	}
}

func (r *GithubIssueReconciler) UpdateIssueStatus(ctx context.Context, issue *issuesv1.GithubIssue, isOpen v1.ConditionStatus, hasPR v1.ConditionStatus) error {
	var (
		openReason string
		pullReason string
	)
	if hasPR == v1.ConditionTrue {
		pullReason = "OpenPullRequest"
	} else {
		pullReason = "NoOpenPullRequest"
	}
	if isOpen == v1.ConditionTrue {
		openReason = "IssueIsOpen"
	} else {
		openReason = "IssueIsClosed"
	}
	hasPRCondition := &v1.Condition{Type: "IssueHasPR", Status: hasPR, Reason: pullReason}
	isOpenCondition := &v1.Condition{Type: "IssueIsOpen", Status: isOpen, Reason: openReason}
	var changed = false
	if !meta.IsStatusConditionPresentAndEqual(issue.Status.Conditions, "IssueIsOpen", isOpen) {
		meta.SetStatusCondition(&issue.Status.Conditions, *isOpenCondition)
		changed = true
	}
	if !meta.IsStatusConditionPresentAndEqual(issue.Status.Conditions, "IssueHasPR", hasPR) {
		meta.SetStatusCondition(&issue.Status.Conditions, *hasPRCondition)
		changed = true
	}
	if changed {
		r.Log.Info("editing Issue status")
		err := r.Client.Status().Update(ctx, issue)
		if err != nil {
			r.Log.Error("failed editing status", zap.Error(err))
			return err

		}

	}
	return nil

}
