# Models

## Start

```sh
export PROFILE=qwen3-14b
docker compose --profile $PROFILE up -d
docker compose --profile $PROFILE logs -f
docker compose --profile $PROFILE down
```

## Test

```sh
curl -s http://localhost:12000/v1/chat/completions -H "Content-Type: application/json" -d '{"model":"qwen3:14b","messages":[{"role":"user","content":"hi"}],"max_tokens":30}' | python3 -m json.tool
```

## Produce

```sh
make run-all K=40 MODEL=qwen3:14b        # the paper run
make render MODEL=qwen3:14b
```
