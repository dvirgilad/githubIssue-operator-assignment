package controller

import (
	issuesv1 "dvir.io/githubissue/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"time"
	//+kubebuilder:scaffold:imports
)

var _ = Describe("githubIssue controller", func() {
	const (
		IssueName      = "testissue"
		IssueNamespace = "testnamespace"
		timeout        = time.Second * 10
		duration       = time.Second * 10
		interval       = time.Millisecond * 250
	)

	Context("When creating githubIssue ", func() {
		It("Receive error when trying to create an issue", func() {
			By("create Issue")
			//ctx := context.Background()
			testIssue := &issuesv1.GithubIssue{
				ObjectMeta: metav1.ObjectMeta{
					Name:      IssueName,
					Namespace: IssueNamespace,
				},
				Spec: issuesv1.GithubIssueSpec{
					Title:       "test",
					Description: "description",
					Repo:        "https://github.com/test/test",
				},
			}
			err := k8sClient.Create(ctx, testIssue)
			Expect(err).ToNot(HaveOccurred())
			createdIssueLookupKey := types.NamespacedName{Name: IssueName, Namespace: IssueNamespace}
			createIssue := &issuesv1.GithubIssue{}
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, createdIssueLookupKey, createIssue)).Should(BeNil(), "should find resource")
				return meta.IsStatusConditionPresentAndEqual(createIssue.Status.Conditions, "IssueIsOpen", metav1.ConditionFalse)
			}).Should(BeTrue())

		})

	})
})

//func testFailedCreatingIssue(t *testing.T) {
//	g := NewGomegaWithT(t)
//	RegisterFailHandler(Fail)
//	ctx := context.Background()
//	s := scheme.Scheme
//	err := issuesv1.AddToScheme(s)
//	g.Expect(err).To(BeNil())
//	//+kubebuilder:scaffold:scheme
//
//	r := &GithubIssueReconciler{k8sClient, s, TestLog, ghClient}
//	r.Reconcile(ctx)
//}
