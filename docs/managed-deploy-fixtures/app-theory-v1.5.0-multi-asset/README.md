# AppTheory v1.5.0 managed multi-asset fixture

This directory is a **non-deployable contract fixture** for lesser-host and other managed consumers. It models the
release shape Body will emit when AppTheory/CDK generates auxiliary file assets, such as the stream-spill S3 auto-delete
custom resource provider Lambda.

Consumers should use this fixture to test parsing, checksum verification, auxiliary asset upload planning, template
parameter derivation, and fail-closed behavior. The `.fixture.txt` files are intentionally text, not runnable Lambda
archives; they exist so checksum and upload logic can be tested without committing generated binary assets.

Key expectations represented here:

- `lesser-body-deploy.json` uses `schema: 2` and declares `required_capabilities: ["managed_auxiliary_assets_v1"]`.
- `auxiliary_assets[]` lists every non-primary Lambda/CDK file asset with path, sha256, bytes, required flag, target
  `s3_key`, and the template parameter that receives the staged object key.
- Stage templates reference the same artifact bucket parameter (`LesserBodyCodeBucketName`) and per-asset object-key
  parameters instead of CDK bootstrap buckets.
- `checksums.txt` covers every fixture asset except itself.

This fixture is the M1 contract artifact for Project 27.
