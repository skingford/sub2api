"""Unmodified pinned CLI, explicit tool controls, synthetic local responses.

EXTRA_BODY cases prove serialization/preservation, not model acceptance.
"""
import json
import comprehensive_lab as lab

SCHEMA = {"type": "object", "properties": {"value": {"type": "string"}},
          "required": ["value"], "additionalProperties": False}
lab.CASES = []
for model in lab.MODELS + ["claude-haiku-5-5"]:
    for name, extra in [
        ("serial-auto", {"tool_choice": {"type": "auto", "disable_parallel_tool_use": True}}),
        ("parallel-auto", {"tool_choice": {"type": "auto", "disable_parallel_tool_use": False}}),
        ("strict-true", {"tools": [{"name": "audit_tool", "input_schema": SCHEMA, "strict": True}]}),
        ("strict-false", {"tools": [{"name": "audit_tool", "input_schema": SCHEMA, "strict": False}]}),
        ("combined", {"tools": [{"name": "audit_tool", "input_schema": SCHEMA, "strict": True}],
                      "tool_choice": {"type": "auto", "disable_parallel_tool_use": True},
                      "output_config": {"format": {"type": "json_schema", "schema": SCHEMA}, "effort": "low"},
                      "stop_sequences": ["STOP_LOCAL"], "max_tokens": 64}),
        ("max64-high", {"max_tokens": 64, "output_config": {"effort": "high"}}),
    ]:
        lab.CASES.append({"name": model + "-" + name, "base": "api-arg", "model": model,
                          "env": {"CLAUDE_CODE_EXTRA_BODY": json.dumps(extra)},
                          "generic_controls": extra})

if __name__ == "__main__":
    lab.main()
