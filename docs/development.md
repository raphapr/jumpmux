# Development

The Go command stays at the module root so `go install github.com/raphapr/jumpmux@latest` works. Source and test filenames follow their features. The Pi extension lives in `extension/`.

Run the full package during development:

```console
go run .
```

Prepare a release from a clean working tree:

```console
make release VERSION=0.0.1
git push origin v0.0.1
```

The Make target tests the project and creates an annotated `v0.0.1` tag on the current commit. Pushing that tag starts the release workflow. The tag is the only release version source; local and source builds report `dev`.
