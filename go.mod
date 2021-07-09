module policy-splitter

go 1.16

require (
	github.com/go-logr/logr v0.4.0
	github.com/open-cluster-management/governance-policy-propagator v0.0.0-20210630164322-97ff9a0a2a3c
	go.uber.org/zap v1.18.1 // indirect
	k8s.io/apimachinery v0.21.2
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.9.2
)

replace k8s.io/client-go => k8s.io/client-go v0.21.2
