SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

IMAGE ?= batchscope
TAG ?= local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
.PHONY: help bootstrap fmt fmt-check scripts-check vet test run openapi openapi-check verify demo-view perf-small perf-medium perf-scale perf-pathological perf-concurrent perf-growth release-artifacts image image-run check-docker

PERF_RUNS ?= 5
PERF_PATHOLOGICAL_RUNS ?= 3
PERF_CONCURRENT_RUNS ?= 5
PERF_GROWTH_RUNS ?= 2
PERF_GROWTH_SIZES ?= 10000:25000 20000:50000 40000:100000 80000:200000
PERF_GROWTH_OUTPUT ?= /tmp/batchscope-perf-growth

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
	bash -n scripts/*.sh

run: ## [Dev Container] サービスを起動する
	go run ./cmd/batchscope serve

openapi: ## [Dev Container] OpenAPIを生成する
	@mkdir -p docs/api
	@set -e; \
		tmp="$$(mktemp docs/api/openapi.yaml.tmp.XXXXXX)"; \
		trap 'rm -f "$$tmp"' EXIT; \
		go run ./cmd/openapi-gen > "$$tmp"; \
		mv "$$tmp" docs/api/openapi.yaml

openapi-check: ## [Dev Container/CI] OpenAPI生成物の差分を確認する
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

perf-medium: ## [Dev Container] Mediumデータの取込と検索性能をJSONで測定する
	@go run ./cmd/perf-measure -profile medium -runs $(PERF_RUNS)

perf-scale: ## [Dev Container] Scaleデータの取込と検索性能をJSONで測定する
	@go run ./cmd/perf-measure -profile scale -runs $(PERF_RUNS)

perf-pathological: ## [Dev Container] 軽量な病理グラフの取込と検索性能をJSONで測定する
	@go run ./cmd/perf-measure -profile pathological -runs $(PERF_PATHOLOGICAL_RUNS)

perf-concurrent: ## [Dev Container] Smallデータで同一SQLiteへの同時検索性能をJSONで測定する
	@go run ./cmd/perf-measure -mode concurrent -profile small -runs $(PERF_CONCURRENT_RUNS) -concurrencies 1,2,4,8

perf-growth: ## [Dev Container] 中間規模の取込と検索性能を規模別のJSONで測定する
	@mkdir -p "$(PERF_GROWTH_OUTPUT)"
	@set -e; \
	for size in $(PERF_GROWTH_SIZES); do \
		nodes="$${size%%:*}"; \
		relations="$${size##*:}"; \
		output="$(PERF_GROWTH_OUTPUT)/$${nodes}-nodes-$${relations}-relations.json"; \
		go run ./cmd/perf-measure -profile custom -nodes "$$nodes" -relations "$$relations" -runs $(PERF_GROWTH_RUNS) > "$$output"; \
		echo "$$output"; \
	done

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
