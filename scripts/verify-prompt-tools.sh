#!/bin/sh
# Measures whether tool calling works per model when tools travel through the
# prompt fallback instead of the MCP shim (oocla serve --internal-prompt-tools).
# The model's decision to call a tool is non-deterministic, so this runs each
# case several times and reports counts.
#
# Calls the real claude CLI and costs money.
#
#   scripts/verify-prompt-tools.sh [models...]   default: opus sonnet haiku fable
set -eu

BIN=${OOCLA_BIN:-bin/oocla}
ADDR=${ADDR:-127.0.0.1:21435}
TRIALS=${TRIALS:-5}
ROUNDTRIPS=${ROUNDTRIPS:-3}
MODELS=${*:-opus sonnet haiku fable}

TOOLS='[{"type":"function","function":{"name":"get_weather","description":"Get current weather for a city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}]'

"$BIN" serve --addr "$ADDR" --internal-prompt-tools &
SRV=$!
trap 'kill "$SRV" 2>/dev/null' EXIT
sleep 0.5

chat() {
	curl -sS --max-time 120 "http://$ADDR/api/chat" -d "$1"
}

for model in $MODELS; do
	called=0
	answered=0
	roundtrip=0

	i=0
	while [ "$i" -lt "$TRIALS" ]; do
		i=$((i + 1))
		res=$(chat '{"model":"'"$model"'","stream":false,
			"messages":[{"role":"user","content":"What is the weather in Tokyo right now? Use the tool."}],
			"tools":'"$TOOLS"'}')
		if [ "$(printf '%s' "$res" | jq -r '.message.tool_calls[0].function.name // empty')" = "get_weather" ]; then
			called=$((called + 1))
		fi
	done

	# The model must also be able to decline the tool and just answer.
	i=0
	while [ "$i" -lt 2 ]; do
		i=$((i + 1))
		res=$(chat '{"model":"'"$model"'","stream":false,
			"messages":[{"role":"user","content":"What is 2+2? Answer with just the number."}],
			"tools":'"$TOOLS"'}')
		if [ "$(printf '%s' "$res" | jq -r '.message.tool_calls | length')" = "0" ] &&
			[ -n "$(printf '%s' "$res" | jq -r '.message.content')" ]; then
			answered=$((answered + 1))
		fi
	done

	# And consume a tool result on the next turn.
	i=0
	while [ "$i" -lt "$ROUNDTRIPS" ]; do
		i=$((i + 1))
		res=$(chat '{"model":"'"$model"'","stream":false,
			"messages":[
				{"role":"user","content":"What is the weather in Tokyo? Use the tool."},
				{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Tokyo"}}}]},
				{"role":"tool","tool_name":"get_weather","content":"{\"temp_c\":22,\"condition\":\"light rain\"}"}],
			"tools":'"$TOOLS"'}')
		if printf '%s' "$res" | jq -r '.message.content' | grep -q "22"; then
			roundtrip=$((roundtrip + 1))
		fi
	done

	printf '%s\ttool_call %d/%d\tdecline %d/2\tround-trip %d/%d\n' \
		"$model" "$called" "$TRIALS" "$answered" "$roundtrip" "$ROUNDTRIPS"
done
