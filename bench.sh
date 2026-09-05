#!/bin/bash

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

if [[ -f "$script_dir/.env" ]]; then
	# shellcheck source=/dev/null
	source "$script_dir/.env"
fi

notify_discord() {
	local message=$1
	local payload

	payload=$(jq -cn --arg content "$message" '{content: $content}') || return
	curl -fsS \
		-H 'Content-Type: application/json' \
		-d "$payload" \
		"$WEBHOOK_URL" >/dev/null ||
		echo 'Discord webhook への通知に失敗しました' >&2
}

if [[ -n "${WEBHOOK_URL:-}" ]]; then
	notify_discord 'ベンチマークスタート'
fi

output_file=$(mktemp)
trap 'rm -f "$output_file"' EXIT

ssh bench "cd ~/isuumo/bench && ./bench --target-url http://isu1:80" | tee "$output_file"
benchmark_status=${PIPESTATUS[0]}

if [[ -n "${WEBHOOK_URL:-}" ]]; then
	benchmark_json=$(tac "$output_file" | while IFS= read -r line; do
		if jq -e . >/dev/null 2>&1 <<<"$line"; then
			printf '%s' "$line"
			break
		fi
	done)

	if [[ -n "$benchmark_json" ]]; then
		notify_discord "ベンチマーク結果
\`\`\`json
$benchmark_json
\`\`\`"
	else
		notify_discord "ベンチマーク終了 (exit status: $benchmark_status)
結果の JSON を取得できませんでした"
	fi
fi

exit "$benchmark_status"
