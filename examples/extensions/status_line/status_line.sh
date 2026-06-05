#!/usr/bin/env bash
# Sample mixi extension in plain shell: sets a footer status segment that
# counts completed turns. Demonstrates hello, event subscription, and the
# set_status action without any JSON library.
set -u

emit() { printf '%s\n' "$1"; }

emit '{"type":"hello","name":"status-line","version":"1.0.0","protocol":1,"subscribe":["session_start","turn_end"]}'

turns=0
while IFS= read -r line; do
  case "$line" in
    *'"type":"shutdown"'*) exit 0 ;;
    *'"event":"session_start"'*)
      emit '{"type":"action","id":1,"action":"set_status","params":{"text":"turns: 0"}}'
      ;;
    *'"event":"turn_end"'*)
      turns=$((turns + 1))
      emit "{\"type\":\"action\",\"id\":$((turns + 1)),\"action\":\"set_status\",\"params\":{\"text\":\"turns: ${turns}\"}}"
      ;;
  esac
done
