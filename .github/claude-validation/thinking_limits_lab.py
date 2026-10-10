"""Pinned CLI token/thinking controls applied before request construction."""
import comprehensive_lab as lab

lab.CASES = []
for model in lab.MODELS + ["claude-haiku-5-5"]:
    limits = [64, 1024, 1025, 4096] if model in lab.MODELS[:3] else [64, 4096]
    for limit in limits:
        lab.CASES.append({"name": model + "-limit" + str(limit) + "-high", "base": "api-arg",
                          "model": model, "args": ["--effort", "high"],
                          "env": {"CLAUDE_CODE_MAX_OUTPUT_TOKENS": str(limit)},
                          "generic_controls": {"max_tokens": limit, "output_config": {"effort": "high"}}})

if __name__ == "__main__":
    lab.main()
