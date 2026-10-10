import { execFileSync } from "node:child_process";

// Pin and verify an official upstream release without relying on removed images.
// An explicit override supports offline test runners with an already mirrored fixture.
export function ensureMinIOFixture() {
  if (process.env.QA_MINIO_IMAGE) return process.env.QA_MINIO_IMAGE;
  const image = "spark-console-minio-fixture:2025-04-22-53e2a2cb";
  try {
    execFileSync("docker", ["image", "inspect", image], { stdio: "ignore" });
  } catch {
    execFileSync(
      "docker",
      [
        "build",
        "--platform",
        "linux/amd64",
        "-f",
        "scripts/fixtures/minio.Dockerfile",
        "-t",
        image,
        ".",
      ],
      { stdio: "inherit" },
    );
  }
  return image;
}
