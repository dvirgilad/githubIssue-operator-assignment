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
	"fmt"
	"strings"

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
			return ctrl.Result{}, nil
		}
	}
	splitUrl := strings.Split(issueObject.Spec.Repo, "/")
	owner := splitUrl[3]
	repo := splitUrl[4]
	log.Info(fmt.Sprintf("attempting to get isues from %s/%s", owner, repo))
	gitHubIssue, err := r.FindIssue(ctx, owner, repo, issueObject)
	if err != nil {
		log.Error("failed fetching issue", zap.Error(err))
		return ctrl.Result{}, err
	}
	// Check if issues is being deleted
	if !issueObject.ObjectMeta.DeletionTimestamp.IsZero() {
		//Issue is being deleted: close it
		log.Info("closing issue")
		if err := r.CloseIssue(ctx, owner, repo, gitHubIssue); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed closing issue: %v", err.Error())
		}
		ok, err := r.DeleteFinalizer(ctx, issueObject)
		if err != nil {
			return ctrl.Result{}, err
		} else {
			if ok {
				return ctrl.Result{}, nil
			}
		}

	}
	//Issue is not being deleted, add finalizer and search for it
	err = r.AddFinalizer(ctx, issueObject)
	if err != nil {
		log.Error("failed adding finalizer!", zap.Error(err))
		return ctrl.Result{}, err
	}

	if gitHubIssue == nil {

		//Issue does not exist, create it
		log.Info("creating issue")
		err := r.CreateIssue(ctx, owner, repo, issueObject)
		if err != nil {
			if statusErr := r.UpdateIssueStatus(ctx, issueObject, gitHubIssue); err != nil {
				log.Error("error updating status ", zap.Error(statusErr))
			}
			return ctrl.Result{}, err
		}
		gitHubIssue, err := r.FindIssue(ctx, owner, repo, issueObject)
		if err != nil {
			log.Error("failed fetching issue", zap.Error(err))
			return ctrl.Result{}, err
		}
		if err := r.UpdateIssueStatus(ctx, issueObject, gitHubIssue); err != nil {
			log.Error("error updating status ", zap.Error(err))
		}
		log.Info("issue created")
		return ctrl.Result{}, nil

	} else {
		//Issue exists, edit if needed and check for a PR
		log.Info("editing issue")

		if err := r.EditIssue(ctx, owner, repo, issueObject, *gitHubIssue.Number); err != nil {
			return ctrl.Result{}, err
		}
		gitHubIssue, err := r.FindIssue(ctx, owner, repo, issueObject)
		if err != nil {
			log.Error("failed fetching issue", zap.Error(err))
			return ctrl.Result{}, err
		}
		if err := r.UpdateIssueStatus(ctx, issueObject, gitHubIssue); err != nil {
			log.Error("error updating status ", zap.Error(err))
		}
		log.Info("issue edited")
		return ctrl.Result{}, nil
	}

}

// SetupWithManager sets up the controller with the Manager.
func (r *GithubIssueReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&issuesv1.GithubIssue{}).
		Complete(r)
}
