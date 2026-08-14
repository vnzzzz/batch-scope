SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

IMAGE ?= batchscope
TAG ?= local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
.PHONY: help bootstrap fmt fmt-check scripts-check vet test run smoke openapi openapi-check verify demo-view perf-small perf-pathological perf-concurrent perf-connection-comparison perf-target-search perf-limit-analysis release-artifacts release-artifacts-check image image-run check-docker

PERF_RUNS ?= 5
PERF_PATHOLOGICAL_RUNS ?= 3
PERF_CONCURRENT_RUNS ?= 5
PERF_CONNECTION_COMPARISON_RUNS ?= 5
PERF_TARGET_SEARCH_RUNS ?= 20
PERF_LIMIT_ANALYSIS_RUNS ?= 5
PERF_LIMIT_ANALYSIS_PROFILE ?= small
PERF_LIMIT_ANALYSIS_CONCURRENCIES ?= 1,4

help: ## [共通] 利用できるターゲットを表示する
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

bootstrap: ## [Dev Container] Goモジュールを取得する
	go mod download

fmt: ## [Dev Container] Goソースを整形する
	gofmt -w $$(git ls-files '*.go')

fmt-check: ## [Dev Container/CI] Goソースの整形を確認する
	@files="$$(gofmt -l $$(git ls-files '*.go'))"; test -z "$$files" || { gofmt -d $$files; exit 1; }

vet: ## [Dev Container/CI] go vetを実行する
	go vet ./...

test: ## [Dev Container/CI] テストを実行する
	go test -race ./...

scripts-check: ## [Dev Container/CI] シェルスクリプトの構文を確認する
	bash -n scripts/*.sh .devcontainer/scripts/*.sh

run: ## [Dev Container] サービスを起動する
	go run ./cmd/batchscope serve

smoke: ## [Dev Container] 公開APIのE2Eスモークテストを実行する
	./scripts/smoke-api.sh

openapi: ## [Dev Container] OpenAPIを生成する
	@mkdir -p docs/api
	@set -e; \
		tmp="$$(mktemp docs/api/openapi.yaml.tmp.XXXXXX)"; \
		trap 'rm -f "$$tmp"' EXIT; \
		go run ./cmd/openapi-gen > "$$tmp"; \
		mv "$$tmp" docs/api/openapi.yaml

openapi-check: ## [Dev Container/CI] OpenAPI生成物と実装の差分を確認する
	@set -e; \
		tmp="$$(mktemp)"; \
		trap 'rm -f "$$tmp"' EXIT; \
		go run ./cmd/openapi-gen > "$$tmp"; \
		diff -u docs/api/openapi.yaml "$$tmp"

verify: fmt-check scripts-check vet test openapi-check ## [Dev Container/CI] 静的検査とテストを実行する

demo-view: ## [Dev Container] デモのAPIレスポンスを読みやすく表示する
	./scripts/show-limit-analysis.sh examples/demo/responses/downstream-limit-analysis.json

perf-small: ## [Dev Container] Smallデータの取込と検索性能をJSONで測定する
	@go run ./cmd/perf-measure -profile small -runs $(PERF_RUNS)

perf-pathological: ## [Dev Container] 軽量な病理グラフの取込と検索性能をJSONで測定する
	@go run ./cmd/perf-measure -profile pathological -runs $(PERF_PATHOLOGICAL_RUNS)

perf-concurrent: ## [Dev Container] Smallデータで同一SQLiteへの同時検索性能をJSONで測定する
	@go run ./cmd/perf-measure -mode concurrent -profile small -runs $(PERF_CONCURRENT_RUNS) -concurrencies 1,2,4,8

perf-connection-comparison: ## [Dev Container] Smallデータで単一接続と複数読み取り接続を比較する
	@go run ./cmd/perf-measure -mode connection-comparison -profile small -runs $(PERF_CONNECTION_COMPARISON_RUNS) -concurrencies 1,2,4,8

perf-target-search: ## [Dev Container] Smallデータで公開HTTPの完全一致検索性能をJSONで測定する
	@go run ./cmd/perf-measure -mode target-search -profile small -runs $(PERF_TARGET_SEARCH_RUNS)

perf-limit-analysis: ## [Dev Container] 公開HTTPの後続リミット取得性能をJSONで測定する
	@go run ./cmd/perf-measure -mode limit-analysis -profile $(PERF_LIMIT_ANALYSIS_PROFILE) -runs $(PERF_LIMIT_ANALYSIS_RUNS) -concurrencies $(PERF_LIMIT_ANALYSIS_CONCURRENCIES)

release-artifacts: ## [Dev Container/CI] GitHub Releasesへ登録するアーカイブを作成する
	./scripts/build-release-artifacts.sh "$(VERSION)" "$(COMMIT)" dist

release-artifacts-check: ## [Dev Container/CI] 作成済みRelease成果物の構成と内容を検査する
	bash ./scripts/check-release-artifacts.sh "$(VERSION)" dist

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
