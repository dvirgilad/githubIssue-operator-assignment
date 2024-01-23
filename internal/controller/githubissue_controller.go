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
	issuesv1 "dvir.io/githubissue/api/v1"
	"fmt"
	"github.com/google/go-github/v58/github"
	"github.com/pkg/errors"
	"go.elastic.co/ecszap"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GithubIssueReconciler reconciles a GithubIssue object
type GithubIssueReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    *zap.Logger
}

const CloseIssuesFinalizer = "issues.dvir.io/finalizer"

//+kubebuilder:rbac:groups=issues.dvir.io,resources=githubissues,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=issues.dvir.io,resources=githubissues/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=issues.dvir.io,resources=githubissues/finalizers,verbs=update

// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *GithubIssueReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	encoderConfig := ecszap.NewDefaultEncoderConfig()
	core := ecszap.NewCore(encoderConfig, os.Stdout, zap.DebugLevel)
	r.Log = zap.New(core, zap.AddCaller())
	log := r.Log
	var issueObject = &issuesv1.GithubIssue{}
	if err := r.Get(ctx, req.NamespacedName, issueObject); err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error("unable to fetch issue object", zap.Error(err))
			return ctrl.Result{}, err
		} else {
			return ctrl.Result{}, nil
		}
	}
	repoUrl, err := isGitHubURL(issueObject.Spec.Repo)
	if err != nil {
		err := errors.New("not a valid git repo")
		log.Error("invalid git repo", zap.Error(err))
		return ctrl.Result{}, err
	}
	gitHubToken := os.Getenv("GITHUB_TOKEN")
	owner := repoUrl.GetOwnerName()
	repo := repoUrl.GetRepoName()
	log.Info(fmt.Sprintf("attempting to get isues from %s/%s", owner, repo))
	ghClient := github.NewClient(nil).WithAuthToken(gitHubToken)
	opt := &github.IssueListByRepoOptions{}
	allIssues, response, err := ghClient.Issues.ListByRepo(ctx, owner, repo, opt)
	if err != nil {
		err := errors.New(fmt.Sprintf("could not fetch issues: status %s", response.Status))
		log.Error("failed fetching issues", zap.Error(err))
		return ctrl.Result{}, err
	}
	log.Info("fetched issues")
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
		_, _, err := ghClient.Issues.Edit(ctx, owner, repo, *gitHubIssue.Number, closedIssueRequest)
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
				return ctrl.Result{}, nil
			}
		}

	} else {
		//Issue is being is not deleted, add finalizer and search for it
		_, err := r.AddFinalizer(ctx, issueObject)
		if err != nil {
			return ctrl.Result{}, err
		}

		gitHubIssue := searchForIssue(issueObject, allIssues)
		if gitHubIssue == nil {

			//Issue does not exist, create it
			log.Info("creating issue")
			newIssue := &github.IssueRequest{Title: &issueObject.Spec.Title, Body: &issueObject.Spec.Description}
			_, response, err := ghClient.Issues.Create(ctx, owner, repo, newIssue)
			if err != nil {
				err := errors.New(fmt.Sprintf("could not create issue: status %s", response.Status))
				log.Error("failed creating issue", zap.Error(err))
				return ctrl.Result{}, err
			}
			if err = r.UpdateIssueStatus(ctx, issueObject, metav1.ConditionTrue, metav1.ConditionFalse); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("issue created")
			return ctrl.Result{}, nil

		} else {
			//Issue exists, edit if needed and check for a PR
			log.Info("editing issue")
			editIssueRequest := &github.IssueRequest{Body: &issueObject.Spec.Description}
			_, response, err := ghClient.Issues.Edit(ctx, owner, repo, *gitHubIssue.Number, editIssueRequest)
			if err != nil {
				err := errors.New(fmt.Sprintf("could not edit issue: status %s", response.Status))
				log.Error("failed editing issue", zap.Error(err))
				return ctrl.Result{}, err
			}
			//opts := &github.ListOptions{}
			tl, _, err := ghClient.Issues.ListIssueTimeline(ctx, owner, repo, *gitHubIssue.Number, nil)
			if err != nil {
				log.Error("error fetching issue timeline", zap.Error(err))
				return ctrl.Result{}, nil
			}

			hasPR := searchForPR(tl)
			if err = r.UpdateIssueStatus(ctx, issueObject, metav1.ConditionTrue, hasPR); err != nil {
				return ctrl.Result{}, err
			}

			log.Info("issue edited")
			return ctrl.Result{}, nil
		}

	}
	return ctrl.Result{}, nil

}

// SetupWithManager sets up the controller with the Manager.
func (r *GithubIssueReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&issuesv1.GithubIssue{}).
		Complete(r)
}
