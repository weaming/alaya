GO_FLAGS = CGO_ENABLED=0
GO_LD = -ldflags "-s -w" -trimpath -tags timetzdata

install:
	go fmt ./...
	$(GO_FLAGS) go install $(GO_LD) ./cmd/alaya
