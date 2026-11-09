# cel-lens for VS Code

Shows CEL errors, hover, completion, quick fixes and colors inside Kubernetes YAML.

## What it looks like

<!-- Picture to take: images/inline-info.png, the line above the rules of test/pictures/widget_crd.yaml in a dark theme, such as `self: Widget.spec · compiles` above `- rule: self.replicas >= self.minReplicas`. -->

The line above each CEL rule: what `self` is, whether the rule compiles, and the values it reads as `dyn`. Click the first part to open the rule's first problem in the problems peek, or its hover when it has none; click **+N more** to pick from the values the line leaves out. **cel-lens: Toggle Inline Info** turns the line off and on.

<!-- Picture to take: images/give-type.png, the picker titled `Give params a type`, opened from `params: dyn · Give it a type` above a rule of a ValidatingAdmissionPolicy whose spec.paramKind names a custom kind, in a dark theme: `Write a struct…` first, then the built-in kinds with their group and version. -->

Click **Give it a type** on the line above a rule to give a `dyn` value a type: write a struct, checked as you type, or pick a built-in kind or a kind of the workspace's CRDs.

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

## Give a value a type

Where Kubernetes gives a CEL value no type, it is `dyn`, and cel-lens can't check, complete or explain it: an `x-kubernetes-int-or-string` field to a CRD's rule, or the `params` of a policy whose `paramKind` is a custom kind. A `.cellens` file says what such a value is; commit it, so everyone gets the same types. cel-lens writes it for you:

1. On the line above the rule, click **Give it a type** after the value (such as `params: dyn · Give it a type`), or **+N more** and then the value. Or run **cel-lens: Give a Value a Type…** and pick one of the values of the open file.
2. The picker is titled after the value, such as **Give params a type**. Pick **Write a struct…** and type the type, such as `{name: string, replicas: int}`: what is wrong shows under the box as you type, Enter sets it, and Escape changes nothing. Or type to find a built-in kind, such as `deploy` for Deployment, or a kind of the workspace's CRDs (listed with the `crd-typed-policies` experimental feature on). A value of a CRD takes `int` or `string`, so the picker offers just those.
3. The entry goes in the nearest `.cellens` at or above the file, or a new one at the root of its workspace folder, and the rules are checked again with the type.

**Edit** opens the same box filled with the type, or the picker on the kind; the box's **Pick a kind…** button turns to the picker, so a struct can become a kind. A struct typed in the picker's filter is where the box starts. **Remove** removes the entry at once. The engine changes `.cellens` in the editor, so **Undo** in the `.cellens` editor takes a change back.

## Commands

The Command Palette lists the commands under **cel-lens**: **cel-lens: Show Logs** on any file, the others on YAML files. None has a keybinding.

| Command                               | What it does                                                                                                               |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| **cel-lens: Give a Value a Type…**    | Lists the values the rules of the open file read as `dyn` or with a type from `.cellens`, and gives the one picked a type. |
| **cel-lens: Toggle Inline Info**      | Turns the line above each CEL rule off and on.                                                                             |
| **cel-lens: Choose Color Style**      | Picks how CEL is colored: CEL, Classic or Off.                                                                             |
| **cel-lens: Pick Kubernetes Version** | Picks the Kubernetes version the results match.                                                                            |
| **cel-lens: Show Actions**            | Opens the status bar menu.                                                                                                 |
| **cel-lens: Restart Engine**          | Restarts the engine, such as after changing `cel-lens.serverPath`.                                                         |
| **cel-lens: Show Logs**               | Opens the **cel-lens** output channel.                                                                                     |

## Settings

| Setting                      | Default | What it does                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| ---------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cel-lens.colorStyle`        | `cel`   | `cel`, `classic` or `off`: the color style of the parts of a CEL expression. Change it with **cel-lens: Choose Color Style** or the status bar menu. Resource scope: user, workspace or workspace folder.                                                                                                                                                                                                                                                                             |
| `cel-lens.inlineInfo`        | `true`  | Shows a line above each CEL rule: what `self` (or a policy's `object`) is, whether the rule compiles or how many problems it has, and the values it reads as `dyn`. Turn it off and on with **cel-lens: Toggle Inline Info** or the status bar menu, which shows **Inline info: on** or **off**; open editors follow at once. The toggle writes the scope that sets the value in force for the open file, or your user settings. Resource scope: user, workspace or workspace folder. |
| `cel-lens.kubernetesVersion` | `""`    | Advanced: the Kubernetes version the results match, such as `1.36`. Empty means the engine's default. The status bar shows the version the engine uses; one it refuses is logged and the engine keeps its default. May restart the engine.                                                                                                                                                                                                                                            |
| `cel-lens.experimental`      | `[]`    | Advanced: the experimental features to turn on, from `kyverno`, `crd-typed-policies` and `fitting-completion`. Empty means none; see [Experimental features](#experimental-features). A name the engine does not know is ignored and reported once in a notification with a button to the setting; a misspelt name gets a suggestion of the closest known one, and when every unknown name has one, a Replace button rewrites those names in the setting at the scope that set them.  |
| `cel-lens.trace.server`      | `off`   | `off`, `messages` or `verbose`: traces the protocol in the **cel-lens** output channel.                                                                                                                                                                                                                                                                                                                                                                                               |
| `cel-lens.serverPath`        | `""`    | Advanced: a `cel-lens-core` binary to use instead of the bundled engine. Empty (the default) uses the bundled one. Machine scope: in a remote window (WSL, SSH, a dev container) set it on the Remote tab of Settings.                                                                                                                                                                                                                                                                |

## Supported Kubernetes versions

The bundled engine matches the API server's CEL environment of these Kubernetes versions:
`1.33`, `1.34`, `1.35`, `1.36` and `1.37`. The default is `1.37`. To pick another, run **cel-lens: Pick Kubernetes Version** or open the status bar menu.

## Experimental features

Every feature is stable or experimental. A stable feature is always on. An experimental feature is off until you list its name in the `cel-lens.experimental` setting.

Stable:

| Feature                                          | What it does                                                                                                                    |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------- |
| Checks of CEL                                    | Compiles the CEL of `x-kubernetes-validations`, ValidatingAdmissionPolicy and MutatingAdmissionPolicy like the API server       |
| Language server                                  | Diagnostics, hover, completion and quick fixes                                                                                  |
| Kubernetes version                               | Results match the version in `cel-lens.kubernetesVersion`                                                                       |
| Typed `object`, `oldObject` and policy variables | Typed from the built-in kinds a policy matches                                                                                  |
| Typed `params` of a built-in kind                | Typed from `spec.paramKind` when it names a built-in kind                                                                       |
| Colors                                           | Colors the parts of CEL in the style of `cel-lens.colorStyle`; a Kyverno policy only with the `kyverno` experimental feature on |

Experimental:

| Name                 | What it does                                                                                                                                                                                                                   | Why it is experimental                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `kyverno`            | Checks, hover and completion for Kyverno `ValidatingPolicy` (`policies.kyverno.io/v1`). Off, these documents are parsed but get no diagnostic, hover or completion. On, the extension colors these documents; off, it does not | Compiled with Kyverno's compiler but never run against Kyverno's engine; only `ValidatingPolicy` is read                                                                                                                                                                                                                                                                                                                                                                                      |
| `crd-typed-policies` | Types `params` of a custom resource, and `object` and `oldObject` of a policy that matches one, from a CustomResourceDefinition found in the workspace. Off, they are `dyn` and hover says the feature is off                  | The workspace scan runs only while the feature is on, at initialize or when the setting turns it on. It runs in the background and open documents are checked again when it ends. It reads at most 20000 YAML files and skips files over 1 MiB, so a CRD past either limit leaves its type `dyn`; the engine warns once at the file cap; a single skipped file is logged on its own line, and many are reported in one summary log line with their count, the limit and the first three names |
| `fitting-completion` | After a dot completion offers only what the receiver has, before a dot only the names of this kind of document, and hover says why a value is `dyn`. Off, completion and hover behave as in v0.17.1                            | Built after v0.17.1 of the engine and not yet proven on real use                                                                                                                                                                                                                                                                                                                                                                                                                              |

Kyverno `ValidatingPolicy` documents are colored only with `kyverno` in `cel-lens.experimental`; off, they keep the plain YAML colors.

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
