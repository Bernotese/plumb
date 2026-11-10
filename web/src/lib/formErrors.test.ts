import { describe, expect, it } from "vitest";
import { ApiError } from "./api";
import { describedBy, pointerErrors } from "./formErrors";

describe("formErrors", () => {
  it("maps 422 errors by pointer, first one wins", () => {
    const err = new ApiError({
      type: "urn:plumb:problem:validation",
      title: "Validation failed",
      status: 422,
      errors: [
        { pointer: "/name", message: "a" },
        { pointer: "/name", message: "b" },
        { pointer: "/value", message: "c" },
      ],
    });
    expect(pointerErrors(err)).toEqual({ "/name": "a", "/value": "c" });
    expect(pointerErrors(new Error("x"))).toEqual({});
    expect(pointerErrors(null)).toEqual({});
  });
  it("describes a field by its error and hint", () => {
    expect(describedBy("f", "bad")).toBe("f-error f-hint");
    expect(describedBy("f", undefined)).toBe("f-hint");
    expect(describedBy("f", undefined, false)).toBeUndefined();
    expect(describedBy("f", "bad", false)).toBe("f-error");
  });
});
