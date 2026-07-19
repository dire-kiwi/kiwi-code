#!/usr/bin/env node
import { print, request, run } from "./common.mjs";

run(async () => {
  print(await request());
});
