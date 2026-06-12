#!/usr/bin/env bun
import { VERSION } from "./index.ts";

const HELP = `tadu — TAsk DUrable. A file-backed task store for agents.

Usage: tadu <command> [options]

  (not implemented yet — see DESIGN.md for the planned command surface)

Options:
  -h, --help       Show this help
  -v, --version    Show version
`;

function main(argv: string[]): number {
  const [command] = argv;
  switch (command) {
    case "-v":
    case "--version":
      console.log(`tadu ${VERSION}`);
      return 0;
    case undefined:
    case "-h":
    case "--help":
      console.log(HELP);
      return 0;
    default:
      console.error(`tadu: unknown command "${command}". Try \`tadu --help\`.`);
      return 1;
  }
}

process.exit(main(Bun.argv.slice(2)));
