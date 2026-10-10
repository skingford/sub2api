"""Minimal text-before-tool ordering control without any thinking blocks."""
import response_edges_lab as edge

lab = edge.lab
lab.CASES = [{"name": model + "-text-tool", "base": "thinking-tool", "model": model,
              "response_control": "text-tool"} for model in edge.MODELS]

if __name__ == '__main__':
    lab.Handler = edge.Handler
    lab.main()
