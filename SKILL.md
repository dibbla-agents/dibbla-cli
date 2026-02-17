# Dibbla CLI Skill

You are an expert in using the `dibbla` command-line tool.

## Tool Description

The `dibbla` CLI is used to scaffold new projects and manage applications, databases, and secrets on the Dibbla platform.

## Authentication

Most commands that interact with the Dibbla platform require an API token. The `dibbla` tool retrieves the token from the `DIBBLA_API_TOKEN` environment variable. Before running commands like `apps`, `db`, `secrets`, or `deploy`, ensure the user has provided a token. If the token is missing, the tool will produce an error message prompting the user to set it. You should inform the user how to get the token from `https://app.dibbla.com/settings/api-tokens` and how to set it either in a `.env` file or as an environment variable.

## Commands

Here is a breakdown of the available commands and their usage:

### `create`

The `create` command scaffolds new Dibbla projects.

#### `create go-worker`

This command creates a new Go worker project from a template.

-   **Usage:** `dibbla create go-worker [name]`
-   **Arguments:**
    -   `name` (optional): The name of the project. If not provided, the tool will prompt for it.
-   **Workflow:**
    1.  The tool checks if Go is installed.
    2.  It asks for the project name if not provided.
    3.  It confirms the creation path.
    4.  It interactively prompts for the following information:
        -   **Hosting type:** Dibbla Cloud or Self-hosted.
        -   **gRPC address:** If self-hosted.
        -   **TLS:** If self-hosted.
        -   **API Token:** The `DIBBLA_API_TOKEN`.
        -   **Frontend:** Whether to include a starter frontend project.
    5.  It creates the project structure.
-   **Example:** `dibbla create go-worker my-awesome-worker`

### `apps`

The `apps` command manages deployed applications.

#### `apps list`

Lists all deployed applications.

-   **Usage:** `dibbla apps list`
-   **Output:** A table with application alias, URL, status, and last deployment date.
-   **Example:** `dibbla apps list`

#### `apps delete`

Deletes a deployed application.

-   **Usage:** `dibbla apps delete <alias>`
-   **Arguments:**
    -   `alias` (required): The alias of the application to delete.
-   **Flags:**
    -   `--yes`, `-y`: Skip the confirmation prompt.
-   **Example:** `dibbla apps delete my-old-app -y`

### `db`

The `db` command manages managed databases on the Dibbla platform.

#### `db list`

Lists all available databases.

-   **Usage:** `dibbla db list [--quiet | -q]`
-   **Flags:**
    -   `--quiet`, `-q`: Only print database names, one per line (for scripting; no "Retrieving...", no "Found N...").
-   **Example:** `dibbla db list` — **Quiet (scripting):** `dibbla db list -q`

#### `db create`

Creates a new database.

-   **Usage:** `dibbla db create [name]`
-   **Arguments:**
    -   `name` (optional): The name for the new database.
-   **Flags:**
    -   `--name <name>`: Alternative way to provide the database name.
-   **Example:** `dibbla db create --name my-new-db`

#### `db delete`

Deletes a database.

-   **Usage:** `dibbla db delete <name> [--yes] [--quiet]`
-   **Arguments:**
    -   `name` (required): The name of the database to delete.
-   **Flags:**
    -   `--yes`, `-y`: Skip the confirmation prompt.
    -   `--quiet`, `-q`: Suppress progress and success output (errors only; for scripting).
-   **Example:** `dibbla db delete my-old-db --yes` — **Quiet (scripting):** `dibbla db delete my-old-db --yes -q`

#### `db dump`

Downloads a dump of a database.

-   **Usage:** `dibbla db dump <name>`
-   **Arguments:**
    -   `name` (required): The name of the database to dump.
-   **Flags:**
    -   `--output <file>`, `-o <file>`: The path to save the dump file to. Defaults to `<name>.dump`.
-   **Example:** `dibbla db dump my-production-db -o backup.dump`

#### `db restore`

Restores a database from a dump file.

-   **Usage:** `dibbla db restore <name>`
-   **Arguments:**
    -   `name` (required): The name of the database to restore.
-   **Flags:**
    -   `--file <path>`, `-f <path>` (required): The path to the dump file to restore from.
-   **Example:** `dibbla db restore my-staging-db --file backup.dump`

### `secrets`

The `secrets` command manages secrets on the Dibbla platform. Secrets can be **global** (omit `--deployment`) or **scoped to a deployment** (use `--deployment <alias>`).

#### `secrets list`

Lists secrets (global or for one deployment).

-   **Usage:** `dibbla secrets list [--deployment <alias> | -d <alias>]`
-   **Flags:**
    -   `--deployment`, `-d`: List only secrets for this deployment. Omit for global secrets.
-   **Output:** A table with name, deployment (or "(global)"), and updated-at.
-   **Example:** `dibbla secrets list` — **Per-app:** `dibbla secrets list -d myapp`

#### `secrets set`

Creates or updates a secret.

-   **Usage:** `dibbla secrets set <name> [value] [--deployment <alias> | -d <alias>]`
-   **Arguments:**
    -   `name` (required): The secret name (e.g. `API_KEY`).
    -   `value` (optional): The secret value. If omitted, the value is read from stdin.
-   **Flags:**
    -   `--deployment`, `-d`: Attach the secret to this deployment. Omit for a global secret.
-   **Example:** `dibbla secrets set API_KEY "my-secret"` — **From stdin:** `echo "secret" | dibbla secrets set API_KEY` — **Per-app:** `dibbla secrets set API_KEY "x" -d myapp`

#### `secrets get`

Prints a secret's value (suitable for piping).

-   **Usage:** `dibbla secrets get <name> [--deployment <alias> | -d <alias>]`
-   **Arguments:**
    -   `name` (required): The secret name.
-   **Flags:**
    -   `--deployment`, `-d`: For a deployment-scoped secret.
-   **Example:** `dibbla secrets get API_KEY` — **Per-app:** `dibbla secrets get API_KEY -d myapp`

#### `secrets delete`

Deletes a secret.

-   **Usage:** `dibbla secrets delete <name> [--deployment <alias>] [--yes | -y]`
-   **Arguments:**
    -   `name` (required): The secret name to delete.
-   **Flags:**
    -   `--deployment`, `-d`: For a deployment-scoped secret.
    -   `--yes`, `-y`: Skip the confirmation prompt.
-   **Example:** `dibbla secrets delete API_KEY --yes` — **Per-app:** `dibbla secrets delete API_KEY -d myapp -y`

### `deploy`

The `deploy` command deploys a project to the Dibbla platform.

-   **Usage:** `dibbla deploy [path]`
-   **Arguments:**
    -   `path` (optional): The path to the project to deploy. Defaults to the current directory.
-   **Flags:**
    -   `--force`, `-f`: Force a redeployment if an application with the same alias already exists.
-   **Example:** `dibbla deploy ./my-app --force`

## General Behavior

- The tool is interactive and will prompt for missing information.
- Always provide clear and direct commands.
- When scripting, use flags like `--yes` to avoid interactive prompts.
- Pay attention to the output for success messages, error details, and status information.
