# llm_check_bot — запуск всего одной командой.
#
#   make up       Ollama (низкий приоритет) + сторож от перегрева + бот и Redis в Docker
#   make down     остановить всё
#   make status   что запущено, температура, пауза модели
#   make logs     лог бота (Ctrl+C — выйти)
#
# Ollama и сторож работают в фоне (переживают закрытие терминала, но не
# перезагрузку). Их логи и pid-файлы — в .run/.

SHELL := /bin/bash
RUN   := .run
MODEL ?= $(shell grep -E '^OLLAMA_MODEL=' .env 2>/dev/null | cut -d= -f2- || true)
MODEL := $(or $(MODEL),gemma4:e2b-mlx)

# Ollama: one model, one request at a time, unload after 2 min idle.
OLLAMA_ENV := OLLAMA_FLASH_ATTENTION=1 OLLAMA_KV_CACHE_TYPE=q8_0 \
              OLLAMA_MAX_LOADED_MODELS=1 OLLAMA_NUM_PARALLEL=1 OLLAMA_KEEP_ALIVE=2m

.PHONY: up down restart status logs ollama ollama-stop guard guard-stop docker docker-stop pull test test-live server-up server-down server-logs server-status deploy

up: ollama guard docker ## запустить всё
	@echo "✅ всё запущено — make status, make logs"

down: docker-stop guard-stop ollama-stop ## остановить всё
	@echo "⏹  всё остановлено"

restart: down up

# --- Ollama (на Mac: в Docker нет доступа к видеочипу Apple) ---------------

ollama:
	@mkdir -p $(RUN)
	@if curl -s -m 1 localhost:11434/api/version >/dev/null; then \
		echo "• ollama уже работает"; \
	else \
		$(OLLAMA_ENV) nohup nice -n 10 taskpolicy -c utility ollama serve >$(RUN)/ollama.log 2>&1 & echo $$! >$(RUN)/ollama.pid; \
		for i in $$(seq 1 30); do curl -s -m 1 localhost:11434/api/version >/dev/null && break; sleep 0.5; done; \
		curl -s -m 1 localhost:11434/api/version >/dev/null && echo "• ollama запущена (низкий приоритет)" || { echo "✗ ollama не запустилась, см. $(RUN)/ollama.log"; exit 1; }; \
	fi
	@ollama list | grep -q "$(MODEL)" || { echo "✗ модели $(MODEL) нет — make pull"; exit 1; }

ollama-stop:
	@pkill -f "ollama serve" && echo "• ollama остановлена" || echo "• ollama не была запущена"
	@rm -f $(RUN)/ollama.pid

pull: ## скачать модель из .env
	ollama pull $(MODEL)

# --- Сторож от перегрева --------------------------------------------------

guard:
	@mkdir -p $(RUN)
	@if pgrep -f "scripts/thermal-guard.sh" >/dev/null; then \
		echo "• сторож уже работает"; \
	else \
		nohup ./scripts/thermal-guard.sh >>$(RUN)/thermal-guard.log 2>&1 & echo $$! >$(RUN)/thermal-guard.pid; \
		echo "• сторож запущен (пауза модели при повышенной температуре)"; \
	fi

guard-stop:
	@pkill -f "scripts/thermal-guard.sh" && echo "• сторож остановлен" || echo "• сторож не был запущен"
	@rm -f $(RUN)/thermal-guard.pid

# --- Бот и Redis в Docker -------------------------------------------------

docker:
	@docker info >/dev/null 2>&1 || { echo "✗ Docker не запущен — откройте Docker Desktop"; exit 1; }
	@docker compose up -d --build --quiet-pull 2>&1 | grep -vE "^\s*$$" | tail -3
	@echo "• бот и redis запущены"

docker-stop:
	@docker compose down 2>&1 | tail -1; echo "• бот и redis остановлены (данные redis сохранены)"

# --- Наблюдение -----------------------------------------------------------

status:
	@curl -s -m 1 localhost:11434/api/version >/dev/null && echo "ollama:  работает ($(MODEL))" || echo "ollama:  остановлена"
	@pgrep -f "scripts/thermal-guard.sh" >/dev/null && echo "сторож:  работает" || echo "сторож:  остановлен"
	@docker compose ps --format '{{.Service}}: {{.Status}}' 2>/dev/null | sed 's/^/docker:  /' || echo "docker:  не запущен"
	@./scripts/thermal-guard.sh --status 2>/dev/null || true

logs:
	docker compose logs -f --tail 50 bot

# --- Тесты ----------------------------------------------------------------

test:
	go test ./...

# Живые тесты с моделью — строго по одному, с паузами, чтобы не перегревать Mac.
test-live: ollama
	OLLAMA_LIVE=1 OLLAMA_MODEL=$(MODEL) OLLAMA_THINK=1 go test ./internal/detector -run 'TestOllamaLive$$' -v -count=1 -p 1 -timeout 20m
	sleep 20
	OLLAMA_LIVE=1 OLLAMA_MODEL=$(MODEL) OLLAMA_THINK=1 go test ./internal/detector -run 'TestOllamaRomanceLive$$' -v -count=1 -p 1 -timeout 20m
	sleep 20
	OLLAMA_LIVE=1 OLLAMA_MODEL=$(MODEL) OLLAMA_THINK=1 go test ./internal/detector -run 'TestOllamaCombinedLive$$' -v -count=1 -p 1 -timeout 20m

# --- Сервер: бот и Redis в Docker, Ollama установлена на сервере ---------------
#
# На своём Mac:  make deploy SERVER=root@1.2.3.4   — скопировать проект и .env, запустить
# На сервере:    make server-up / server-down / server-logs / server-status

SERVER_COMPOSE := docker compose -f docker-compose.server.yml
SERVER_DIR     ?= llm_check_bot

server-up:
	$(SERVER_COMPOSE) up -d --build
	@echo "✅ запущено — make server-logs"

server-down:
	$(SERVER_COMPOSE) down

server-logs:
	$(SERVER_COMPOSE) logs -f --tail 50 bot

server-status:
	@$(SERVER_COMPOSE) ps --format '{{.Service}}: {{.Status}}'
	@ollama ps 2>/dev/null || true

# Copies the project (with .env, without .git/data/.run and the Mac-only
# thermal guard) to the server and
# starts it there. Needs SSH access: SERVER=root@<ip>.
deploy:
	@test -n "$(SERVER)" || { echo "usage: make deploy SERVER=root@<ip>"; exit 1; }
	rsync -az --delete --exclude .git --exclude .idea --exclude data --exclude .run \
		--exclude llm_check_bot --exclude scripts/thermal-guard.sh ./ $(SERVER):$(SERVER_DIR)/
	ssh $(SERVER) 'cd $(SERVER_DIR) && make server-up'
