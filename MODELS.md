# Models
* minimax-m2.7
* glm-4.5-air
* codellama
* qwen2.5
* gemma-4
* lfm2.5
* ministral-3

## Start

```sh
docker compose --profile minimax up -d
docker compose --profile minimax logs -f
docker compose --profile minimax down
```

## Test

```sh
curl -s http://localhost:12000/v1/chat/completions -H "Content-Type: application/json" -d '{"model":"qwen2.5","messages":[{"role":"user","content":"hi"}],"max_tokens":30}' | python3 -m json.tool
```
