/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	issuesv1 "dvir.io/githubissue/api/v1"
	"github.com/google/go-github/v56/github"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GithubIssueReconciler reconciles a GithubIssue object
type GithubIssueReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Log          *zap.Logger
	GitHubClient *github.Client
}

const ResyncDuration = 1 * time.Minute

const CloseIssuesFinalizer = "issues.dvir.io/finalizer"

//+kubebuilder:rbac:groups=issues.dvir.io,resources=githubissues,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=issues.dvir.io,resources=githubissues/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=issues.dvir.io,resources=githubissues/finalizers,verbs=update

// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *GithubIssueReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	log := r.Log
	var issueObject = &issuesv1.GithubIssue{}
	if err := r.Get(ctx, req.NamespacedName, issueObject); err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error("unable to fetch issue object", zap.Error(err))
			return ctrl.Result{}, err
		} else {
			return ctrl.Result{RequeueAfter: ResyncDuration}, nil
		}
	}
	repoUrl, err := isGitHubURL(issueObject.Spec.Repo)
	if err != nil {
		err := errors.New("not a valid git repo")
		log.Error("invalid git repo", zap.Error(err))
		return ctrl.Result{}, err
	}
	owner := repoUrl.GetOwnerName()
	repo := repoUrl.GetRepoName()
	log.Info(fmt.Sprintf("attempting to get isues from %s/%s", owner, repo))
	allIssues, err := r.fetchAllIssues(ctx, owner, repo)
	if err != nil {
		return ctrl.Result{}, err
	}
	gitHubIssue := searchForIssue(issueObject, allIssues)
	// Check if issues is being deleted
	if !issueObject.ObjectMeta.DeletionTimestamp.IsZero() {
		//Issue is being deleted: close it
		log.Info("closing issue")
		if gitHubIssue == nil {
			err := errors.New("could not find issue in repo")
			log.Error("error not found", zap.Error(err))
			return ctrl.Result{}, err
		}
		state := "closed"
		closedIssueRequest := &github.IssueRequest{State: &state}
		_, _, err := r.GitHubClient.Issues.Edit(ctx, owner, repo, *gitHubIssue.Number, closedIssueRequest)
		if err != nil {
			err := errors.New("could not close issue")
			log.Error("error closing issue", zap.Error(err))
			return ctrl.Result{}, err
		}
		ok, err := r.DeleteFinalizer(ctx, issueObject)
		if err != nil {
			return ctrl.Result{}, err
		} else {
			if ok {
				return ctrl.Result{RequeueAfter: ResyncDuration}, nil
			}
		}

	} else {
		//Issue is not being deleted, add finalizer and search for it
		err := r.AddFinalizer(ctx, issueObject)
		if err != nil {
			return ctrl.Result{}, err
		}

		if gitHubIssue == nil {

			//Issue does not exist, create it
			log.Info("creating issue")
			newIssue := &github.IssueRequest{Title: &issueObject.Spec.Title, Body: &issueObject.Spec.Description}
			_, response, err := r.GitHubClient.Issues.Create(ctx, owner, repo, newIssue)
			if err != nil {

				log.Error(fmt.Sprintf("failed creating issue: status %s", response.Status), zap.Error(err))
				r.UpdateIssueStatus(ctx, issueObject, metav1.ConditionFalse, metav1.ConditionFalse)
				return ctrl.Result{}, err
			}
			r.UpdateIssueStatus(ctx, issueObject, metav1.ConditionTrue, metav1.ConditionFalse)
			log.Info("issue created")
			return ctrl.Result{RequeueAfter: ResyncDuration}, nil

		} else {
			//Issue exists, edit if needed and check for a PR
			log.Info("editing issue")
			editIssueRequest := &github.IssueRequest{Body: &issueObject.Spec.Description}
			_, response, err := r.GitHubClient.Issues.Edit(ctx, owner, repo, *gitHubIssue.Number, editIssueRequest)
			if err != nil {
				log.Error(fmt.Sprintf("could not edit issue: status %s", response.Status), zap.Error(err))
				r.UpdateIssueStatus(ctx, issueObject, metav1.ConditionTrue, metav1.ConditionFalse)
				return ctrl.Result{}, err
			}
			tl, _, err := r.GitHubClient.Issues.ListIssueTimeline(ctx, owner, repo, *gitHubIssue.Number, nil)
			if err != nil {
				log.Error("error fetching issue timeline", zap.Error(err))
				return ctrl.Result{RequeueAfter: ResyncDuration}, nil
			}

			hasPR := searchForPR(tl)
			r.UpdateIssueStatus(ctx, issueObject, metav1.ConditionTrue, hasPR)

			log.Info("issue edited")
			return ctrl.Result{RequeueAfter: ResyncDuration}, nil
		}

	}
	return ctrl.Result{RequeueAfter: ResyncDuration}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GithubIssueReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&issuesv1.GithubIssue{}).
		Complete(r)
}
