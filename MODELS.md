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
export MODEL=codellama
docker compose --profile $MODEL up -d
docker compose --profile $MODEL logs -f
docker compose --profile $MODEL down
```

## Test

```sh
curl -s http://localhost:12000/v1/chat/completions -H "Content-Type: application/json" -d '{"model":"'"$MODEL"'","messages":[{"role":"user","content":"hi"}],"max_tokens":30}' | python3 -m json.tool
```
