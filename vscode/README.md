# cel-lens for VS Code

Shows CEL errors, hover, completion, quick fixes and colors inside Kubernetes YAML.

## What it looks like

![Colors: a CEL rule colored part by part as it is typed in a CRD](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/colors.gif)

A CEL rule colored part by part as it is typed in a CRD.

![Completion: the list after typing self. and one item picked](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/completion.gif)

The list after typing `self.` and one item picked.

![Problems: a red squiggle under the part of a CEL rule that is wrong, with the reason](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/problems.png)

A red squiggle under the part of a CEL rule that is wrong, with the reason.

![Hover: the type and description of a field in a CEL rule](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/hover.png)

The type and description of a field in a CEL rule.

![Completion: the fields of the schema offered after typing self.](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/completion.png)

The fields of the schema offered after typing `self.`.

![Quick fix: the fix offered on a problem in a CEL rule](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/quick-fix.png)

The fix offered on a problem in a CEL rule.

![Colors: the parts of the CEL expressions of an admission policy in different colors](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/colors.png)

The parts of the CEL expressions of an admission policy in different colors.

## Install

The only way to install today is the `.vsix` from the GitHub releases of JairJosafath/cel-lens:

1. Open <https://github.com/JairJosafath/cel-lens/releases> and pick the release `vscode-vX.Y.Z` you want.
2. Download the `.vsix` for your platform (one file per platform).
3. Run **Extensions: Install from VSIX...** in the Command Palette, or `code --install-extension <file>.vsix`.

The extension carries the cel-lens engine for your platform, so there is nothing else to install or set up.

## Settings

| Setting                      | Default | What it does                                                                                                                                                                                                                               |
| ---------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `cel-lens.colorStyle`        | `cel`   | `cel`, `classic` or `off`: the color style of the parts of a CEL expression. Change it with **cel-lens: Choose Color Style** or the status bar menu. Resource scope: user, workspace or workspace folder.                                  |
| `cel-lens.kubernetesVersion` | `""`    | Advanced: the Kubernetes version the results match, such as `1.36`. Empty means the engine's default. The status bar shows the version the engine uses; one it refuses is logged and the engine keeps its default. May restart the engine. |
| `cel-lens.trace.server`      | `off`   | `off`, `messages` or `verbose`: traces the protocol in the **cel-lens** output channel.                                                                                                                                                    |
| `cel-lens.serverPath`        | `""`    | Advanced: a `cel-lens-core` binary to use instead of the bundled engine. Empty (the default) uses the bundled one. Machine scope: in a remote window (WSL, SSH, a dev container) set it on the Remote tab of Settings.                     |

## Supported Kubernetes versions

The bundled engine matches the API server's CEL environment of these Kubernetes versions:
`1.33`, `1.34`, `1.35`, `1.36` and `1.37`. The default is `1.37`. To pick another, run **cel-lens: Pick Kubernetes Version** or open the status bar menu.

## Experimental features

An experimental feature is off until you name it in the `cel-lens.experimental` setting. The table of which feature is which arrives with that setting.

## Not supported yet

- Dry runs of CEL against sample objects.
- Template tests for CEL.
- Helm templates: YAML with template syntax is not analysed.
- Kubebuilder markers in Go files (`+kubebuilder:validation:XValidation`): only YAML files are covered.
- Untitled YAML buffers: they are not analysed until saved.

## License

cel-lens for VS Code is licensed under Apache-2.0. The full text is in `LICENSE`, which the `.vsix` also carries.

## Contributing

Bug reports and pull requests are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) shows how to build, test and run the extension from source. Report bugs at <https://github.com/JairJosafath/cel-lens/issues>.
