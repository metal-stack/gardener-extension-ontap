package trident

import (
	ontapv1 "github.com/metal-stack/ontap-go/api/client"
	mockcluster "github.com/metal-stack/ontap-go/test/mocks/cluster"
	mocknetworking "github.com/metal-stack/ontap-go/test/mocks/networking"
	mocksecurity "github.com/metal-stack/ontap-go/test/mocks/security"
	mocksvm "github.com/metal-stack/ontap-go/test/mocks/s_vm"
	mockstorage "github.com/metal-stack/ontap-go/test/mocks/storage"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockOntapClient struct {
	client     *ontapv1.Ontap
	svm        *mocksvm.ClientService
	storage    *mockstorage.ClientService
	cluster    *mockcluster.ClientService
	networking *mocknetworking.ClientService
	security   *mocksecurity.ClientService
	k8sClient  client.Client
}

func newMockOntapClient() *mockOntapClient {
	s := &mocksvm.ClientService{}
	st := &mockstorage.ClientService{}
	cl := &mockcluster.ClientService{}
	n := &mocknetworking.ClientService{}
	sec := &mocksecurity.ClientService{}
	k8s := fake.NewClientBuilder().Build()
	return &mockOntapClient{
		client:     &ontapv1.Ontap{SVM: s, Storage: st, Cluster: cl, Networking: n, Security: sec},
		svm:        s,
		storage:    st,
		cluster:    cl,
		networking: n,
		security:   sec,
		k8sClient:  k8s,
	}
}
