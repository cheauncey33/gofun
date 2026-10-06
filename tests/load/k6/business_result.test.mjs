import { test } from "node:test";
import { strict as assert } from "node:assert";
import { classifyResponse } from "./business_result.mjs";

test("business status is classified by HTTP status and business code", () => {
  for (const [status, code, result] of [
    [200, 0, "accepted"], [409, 60001, "sold_out"],
    [400, 40000, "business_rejected"], [400, 60009, "business_rejected"],
    [409, 40900, "business_rejected"], [429, 42900, "rate_limited"],
    [500, 50000, "server_error"], [0, null, "transport_error"],
    [200, 50000, "unexpected"], [429, 60001, "unexpected"],
  ]) assert.equal(classifyResponse(status, { code }), result);
});
