# Deployment

Fork this repository, add a handful of GitHub Actions variables and secrets, and
`.github/workflows/deploy.yml` deploys your own instance on every push to
`master`/`main` (or on demand from the Actions tab).

The workflow is gated on the repository variable `DEPLOY_TARGET`:

| `DEPLOY_TARGET` | Result |
|---|---|
| unset | Both jobs are skipped. A plain fork stays green with no configuration |
| `cloudrun` | Deploys to Google Cloud Run |
| `lambda` | Deploys to AWS Lambda |

Variables live under **Settings → Secrets and variables → Actions → Variables**,
secrets under the **Secrets** tab of the same page.

## Fork and deploy

1. Fork the repository.
2. Provision the target platform once (see the sections below). Cloud Run needs
   no resource created up front; Lambda does.
3. Set `DEPLOY_TARGET` and the variables and secrets for your platform.
4. Push to `master`/`main`, or run the **Deploy** workflow manually from the
   Actions tab.
5. Register the resulting URL as a webhook in App Store Connect and send a test
   ping.

## Application settings

These apply to both platforms. They are forwarded to the deployed service as
environment variables; see the [Configuration](../README.md#configuration)
section of the README for their meaning.

| Name | Kind | Required | Description |
|---|---|---|---|
| `ASC_WEBHOOK_SECRET` | secret | yes | Shared secret you generate yourself and also register with the webhook |
| `SLACK_WEBHOOK_URL` | secret | one of | Slack Incoming Webhook URL |
| `SLACK_BOT_TOKEN` | secret | one of | Slack bot token for `chat.postMessage`, used with `SLACK_CHANNEL` |
| `SLACK_CHANNEL` | variable | one of | Target channel, e.g. `#releases` |
| `WEBHOOK_PATH` | variable | no | Receive path. Defaults to `/webhook` |
| `HEALTH_PATH` | variable | no | Health check path. Defaults to `/health` |
| `NOTIFY_PING` | variable | no | `false` acknowledges pings without notifying Slack |
| `LOG_LEVEL` | variable | no | `debug`, `info`, `warn` or `error` |

The deploy job fails early when `ASC_WEBHOOK_SECRET` is missing, or when neither
`SLACK_WEBHOOK_URL` nor the `SLACK_BOT_TOKEN` + `SLACK_CHANNEL` pair is set.
Optional values are only forwarded when non-empty, so the service falls back to
its own defaults.

`ASC_WEBHOOK_SECRET` is not issued by Apple. Generate it yourself and use the
identical value in two places: this GitHub secret, and the `attributes.secret`
field when you register the webhook with `POST /v1/webhooks` (see below).

```sh
openssl rand -hex 32
```

If the two copies do not match, App Store Connect records every delivery as
`401`.

## Cloud Run

### Settings

| Name | Kind | Required | Default | Description |
|---|---|---|---|---|
| `DEPLOY_TARGET` | variable | yes | – | Must be `cloudrun` |
| `GCP_PROJECT_ID` | variable | yes | – | Google Cloud project id |
| `GCP_REGION` | variable | no | `asia-northeast1` | Cloud Run region |
| `CLOUD_RUN_SERVICE` | variable | no | `asc-slack-notifier` | Service name |
| `GCP_WORKLOAD_IDENTITY_PROVIDER` | secret | yes | – | Full provider resource name |
| `GCP_SERVICE_ACCOUNT` | secret | yes | – | Service account the workflow impersonates |

The job runs `gcloud run deploy --source .`, which builds the container with
Cloud Build and creates the service on the first run, updating it afterwards.
The service is deployed with `--allow-unauthenticated` because App Store Connect
posts to it anonymously; the `x-apple-signature` HMAC is what authenticates the
request.

### One-time setup

Enable the APIs and create the deploy service account:

```sh
PROJECT_ID=your-project
PROJECT_NUMBER=$(gcloud projects describe "$PROJECT_ID" --format 'value(projectNumber)')

gcloud services enable \
  run.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com \
  iamcredentials.googleapis.com --project "$PROJECT_ID"

gcloud iam service-accounts create asc-slack-notifier-deployer --project "$PROJECT_ID"
SA="asc-slack-notifier-deployer@$PROJECT_ID.iam.gserviceaccount.com"

for role in roles/run.admin roles/cloudbuild.builds.editor \
            roles/artifactregistry.admin roles/storage.admin \
            roles/iam.serviceAccountUser; do
  gcloud projects add-iam-policy-binding "$PROJECT_ID" \
    --member "serviceAccount:$SA" --role "$role"
done
```

Set up Workload Identity Federation so GitHub Actions can impersonate it without
a JSON key:

```sh
GITHUB_REPO=your-name/asc-slack-notifier

gcloud iam workload-identity-pools create github \
  --project "$PROJECT_ID" --location global --display-name "GitHub Actions"

gcloud iam workload-identity-pools providers create-oidc github \
  --project "$PROJECT_ID" --location global --workload-identity-pool github \
  --issuer-uri "https://token.actions.githubusercontent.com" \
  --attribute-mapping "google.subject=assertion.sub,attribute.repository=assertion.repository" \
  --attribute-condition "assertion.repository == '$GITHUB_REPO'"

gcloud iam service-accounts add-iam-policy-binding "$SA" \
  --project "$PROJECT_ID" \
  --role roles/iam.workloadIdentityUser \
  --member "principalSet://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/github/attribute.repository/$GITHUB_REPO"

# The value for the GCP_WORKLOAD_IDENTITY_PROVIDER secret
echo "projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/github/providers/github"
```

The attribute condition restricts the provider to your fork; without it any
GitHub repository could assume the service account.

Reference: [google-github-actions/auth](https://github.com/google-github-actions/auth#setting-up-workload-identity-federation).

The workflow prints the deployed service URL to the job summary. Fetch it later
with:

```sh
gcloud run services describe asc-slack-notifier \
  --region asia-northeast1 --format 'value(status.url)'
```

## AWS Lambda + API Gateway

### Settings

| Name | Kind | Required | Default | Description |
|---|---|---|---|---|
| `DEPLOY_TARGET` | variable | yes | – | Must be `lambda` |
| `AWS_REGION` | variable | no | `ap-northeast-1` | Region of the function |
| `LAMBDA_FUNCTION_NAME` | variable | no | `asc-slack-notifier` | Function name |
| `AWS_ROLE_ARN` | secret | yes | – | IAM role GitHub Actions assumes via OIDC |

The job builds `dist/asc-slack-notifier-lambda-arm64.zip` with `make lambda-zip`,
calls `aws lambda update-function-code`, waits for the function to leave the
`InProgress` state, and then applies the environment variables with
`aws lambda update-function-configuration`.

Unlike Cloud Run, `update-function-code` cannot create the function, so the
function and its API Gateway route are provisioned once by hand.

### One-time setup: the function

```sh
AWS_REGION=ap-northeast-1
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
FUNCTION_NAME=asc-slack-notifier

# Execution role for the function itself
aws iam create-role \
  --role-name "$FUNCTION_NAME-execution" \
  --assume-role-policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Principal": { "Service": "lambda.amazonaws.com" },
      "Action": "sts:AssumeRole"
    }]
  }'

aws iam attach-role-policy \
  --role-name "$FUNCTION_NAME-execution" \
  --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole

# Initial deployment package
make lambda-zip

aws lambda create-function \
  --function-name "$FUNCTION_NAME" \
  --runtime provided.al2023 \
  --architectures arm64 \
  --handler bootstrap \
  --role "arn:aws:iam::$ACCOUNT_ID:role/$FUNCTION_NAME-execution" \
  --zip-file fileb://dist/asc-slack-notifier-lambda-arm64.zip \
  --timeout 15 \
  --memory-size 128 \
  --environment 'Variables={ASC_WEBHOOK_SECRET=placeholder,SLACK_WEBHOOK_URL=placeholder}'
```

The placeholder values are replaced by the first workflow run.

### One-time setup: API Gateway

Quick-create an HTTP API whose `$default` route forwards every request to the
function:

```sh
API_ID=$(aws apigatewayv2 create-api \
  --name "$FUNCTION_NAME" \
  --protocol-type HTTP \
  --target "arn:aws:lambda:$AWS_REGION:$ACCOUNT_ID:function:$FUNCTION_NAME" \
  --query ApiId --output text)

aws lambda add-permission \
  --function-name "$FUNCTION_NAME" \
  --statement-id apigateway-invoke \
  --action lambda:InvokeFunction \
  --principal apigateway.amazonaws.com \
  --source-arn "arn:aws:execute-api:$AWS_REGION:$ACCOUNT_ID:$API_ID/*/*"

echo "Webhook URL: https://$API_ID.execute-api.$AWS_REGION.amazonaws.com/webhook"
```

Quick-create uses payload format 2.0, which is what the service expects. Bodies
that API Gateway base64-encodes are decoded before the signature is verified.

### One-time setup: the GitHub Actions OIDC role

Register GitHub's OIDC provider in the account (once per account):

```sh
aws iam create-open-id-connect-provider \
  --url https://token.actions.githubusercontent.com \
  --client-id-list sts.amazonaws.com \
  --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1
```

Create the role the workflow assumes. Replace `your-name/asc-slack-notifier`
with your fork:

```sh
cat > trust-policy.json <<'JSON'
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {
      "Federated": "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
    },
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
      "StringEquals": {
        "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
      },
      "StringLike": {
        "token.actions.githubusercontent.com:sub": "repo:your-name/asc-slack-notifier:ref:refs/heads/*"
      }
    }
  }]
}
JSON

aws iam create-role \
  --role-name "$FUNCTION_NAME-deployer" \
  --assume-role-policy-document file://trust-policy.json

cat > deploy-policy.json <<JSON
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "lambda:UpdateFunctionCode",
      "lambda:UpdateFunctionConfiguration",
      "lambda:GetFunction",
      "lambda:GetFunctionConfiguration",
      "lambda:PublishVersion"
    ],
    "Resource": "arn:aws:lambda:$AWS_REGION:$ACCOUNT_ID:function:$FUNCTION_NAME"
  }]
}
JSON

aws iam put-role-policy \
  --role-name "$FUNCTION_NAME-deployer" \
  --policy-name deploy \
  --policy-document file://deploy-policy.json

echo "AWS_ROLE_ARN: arn:aws:iam::$ACCOUNT_ID:role/$FUNCTION_NAME-deployer"
```

The `sub` condition scopes the role to branch pushes in your fork. Narrow it to
`repo:your-name/asc-slack-notifier:ref:refs/heads/main` if you deploy from a
single branch.

## Register the webhook with App Store Connect

There is no URL verification challenge to answer: Apple does not call the
endpoint to prove ownership before enabling a webhook. Authenticity comes from
the HMAC-SHA256 signature on every delivery, and connectivity is confirmed with
an explicit ping. Registering a URL that is not yet reachable is therefore
harmless — deliveries simply fail and are retried.

Create the webhook with the same secret the service is configured with — the one
you generated with `openssl rand -hex 32` above, not a value from Apple.
`$TOKEN` is a JWT for your App Store Connect API key and `$APP_ID` is the app's
resource id:

```sh
curl -sS -X POST 'https://api.appstoreconnect.apple.com/v1/webhooks' \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "data": {
      "type": "webhooks",
      "attributes": {
        "name": "Slack notifier",
        "url": "https://your-service.example.com/webhook",
        "secret": "your-webhook-secret",
        "enabled": true,
        "eventTypes": [
          "APP_STORE_VERSION_APP_VERSION_STATE_UPDATED",
          "BUILD_UPLOAD_STATE_UPDATED",
          "BUILD_BETA_DETAIL_EXTERNAL_BUILD_STATE_UPDATED",
          "BETA_FEEDBACK_CRASH_SUBMISSION_CREATED",
          "BETA_FEEDBACK_SCREENSHOT_SUBMISSION_CREATED"
        ]
      },
      "relationships": {
        "app": { "data": { "type": "apps", "id": "'"$APP_ID"'" } }
      }
    }
  }'
```

The full list of event types is in the
[README](../README.md#register-the-webhook-with-app-store-connect).

## Test with a ping

```sh
curl -sS -X POST 'https://api.appstoreconnect.apple.com/v1/webhookPings' \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "data": {
      "type": "webhookPings",
      "relationships": {
        "webhook": { "data": { "type": "webhooks", "id": "'"$WEBHOOK_ID"'" } }
      }
    }
  }'
```

The service answers `200` and posts a short "webhook ping received" message to
Slack. Set the `NOTIFY_PING` variable to `false` to acknowledge pings quietly.

Inspect what App Store Connect saw with
`GET /v1/webhooks/{id}/deliveries`. A quick local check that does not involve
Apple at all:

```sh
curl -i https://your-service.example.com/health   # 200 ok
```

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Deliveries recorded as `401` | The secret in App Store Connect differs from `ASC_WEBHOOK_SECRET` |
| Deliveries recorded as `404` | The registered URL does not match `WEBHOOK_PATH` |
| Health checks failing on Cloud Run | `/healthz` can be intercepted by the Google frontend; the default `/health` avoids this |
| Deliveries recorded as `502` | Slack rejected the message — check the service logs for the Slack error, e.g. `channel_not_found` or a revoked Incoming Webhook |
| Both deploy jobs skipped | `DEPLOY_TARGET` is unset or misspelled; it must be exactly `cloudrun` or `lambda` |
| `ResourceConflictException` on Lambda | A concurrent update was in flight. Re-run the workflow; the job waits for `function-updated-v2` between the two calls |
