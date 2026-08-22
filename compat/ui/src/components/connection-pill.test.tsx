import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ConnectionPill } from "./connection-pill";

describe("ConnectionPill", () => {
  it("shows Live when open", () => {
    render(<ConnectionPill connection={{ status: "open", attempt: 0 }} />);
    expect(screen.getByText("Live")).toBeInTheDocument();
  });

  it("shows Connecting… on the first attempt", () => {
    render(
      <ConnectionPill connection={{ status: "connecting", attempt: 0 }} />,
    );
    expect(screen.getByText("Connecting…")).toBeInTheDocument();
  });

  it("shows Reconnecting… with the attempt count in the tooltip while dropped", () => {
    render(
      <ConnectionPill connection={{ status: "reconnecting", attempt: 3 }} />,
    );
    expect(screen.getByText("Reconnecting…")).toBeInTheDocument();
    expect(screen.getByTitle(/attempt 3/)).toBeInTheDocument();
  });
});
