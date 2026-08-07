SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

IMAGE ?= batchscope
TAG ?= local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
.PHONY: help bootstrap fmt fmt-check scripts-check vet test run verify demo-view release-artifacts image image-run check-docker

help: ## [共通] 利用できるターゲットを表示する
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

bootstrap: ## [Dev Container] Goモジュールを取得する
	go mod download

fmt: ## [Dev Container] Goソースを整形する
	gofmt -w $$(find cmd internal -name '*.go' -type f)

fmt-check: ## [Dev Container/CI] Goソースの整形を確認する
	@test -z "$$(gofmt -l $$(find cmd internal -name '*.go' -type f))" || { gofmt -d $$(gofmt -l $$(find cmd internal -name '*.go' -type f)); exit 1; }

vet: ## [Dev Container/CI] go vetを実行する
	go vet ./...

test: ## [Dev Container/CI] テストを実行する
	go test -race ./...

scripts-check: ## [Dev Container/CI] シェルスクリプトの構文を確認する
	bash -n scripts/*.sh

run: ## [Dev Container] サービスを起動する
	go run ./cmd/batchscope serve

verify: fmt-check scripts-check vet test ## [Dev Container/CI] 静的検査とテストを実行する

demo-view: ## [Dev Container] デモのAPIレスポンスを読みやすく表示する
	./scripts/show-limit-analysis.sh examples/demo/responses/downstream-limit-analysis.json

release-artifacts: ## [Dev Container/CI] GitHub Releasesへ登録するバイナリを作成する
	./scripts/build-release-artifacts.sh "$(VERSION)" "$(COMMIT)" dist

check-docker:
	@command -v docker >/dev/null 2>&1 || { echo 'Dockerを利用できるホスト上で実行してください。Dev ContainerにはDocker CLIとソケットを追加していません。' >&2; exit 1; }
	@docker info >/dev/null 2>&1 || { echo 'Dockerデーモンへ接続できません。ホスト上でDockerを起動してください。' >&2; exit 1; }

image: check-docker ## [ホスト/CI] 本番用コンテナイメージを作成する
	docker build \
		--target runtime \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--tag $(IMAGE):$(TAG) \
		.

image-run: check-docker ## [ホスト] 作成済みの本番用イメージを起動する
	docker run --rm --init \
		--cap-drop=ALL \
		--security-opt=no-new-privileges \
		-p 8080:8080 \
		$(IMAGE):$(TAG)
