#!/usr/bin/env python3
import os
import re
import sys
import datetime
from pathlib import Path


def parse_markdown_table(file_path):
    """Parses a markdown table into a dictionary of {tool: version}."""
    tools = {}
    path = Path(file_path)
    if not path.exists():
        print(f"Warning: {file_path} not found.")
        return tools

    with open(file_path, "r") as f:
        for line in f:
            match = re.match(r"^|\s*(.*?)\s*|\s*(.*?)\s*|$", line)
            if match:
                tool = match.group(1).strip()
                version = match.group(2).strip()
                # Skip header and separator
                if tool.lower() in ["tool", ":---"]:
                    continue
                tools[tool] = version
    return tools


def main():
    if len(sys.argv) < 3:
        print("Usage: merge_tool_versions.py <amd64_file> <arm64_file> <output_file>")
        sys.exit(1)

    amd64_file = sys.argv[1]
    arm64_file = sys.argv[2]
    output_file = sys.argv[3]

    amd64_tools = parse_markdown_table(amd64_file)
    arm64_tools = parse_markdown_table(arm64_file)

    # Get all unique tools in order of appearance
    all_tools = []
    seen = set()

    # Maintain order from files
    for tools in [amd64_tools, arm64_tools]:
        for tool in tools:
            if tool not in seen:
                all_tools.append(tool)
                seen.add(tool)

    with open(output_file, "w") as f:
        f.write("# Image Tool Versions (Unified)\n\n")
        f.write("| Tool | amd64 | arm64 |\n")
        f.write("| :--- | :--- | :--- |\n")

        for tool in all_tools:
            v_amd64 = amd64_tools.get(tool, "Not found")
            v_arm64 = arm64_tools.get(tool, "Not found")
            f.write(f"| {tool} | {v_amd64} | {v_arm64} |\n")


if __name__ == "__main__":
    main()
