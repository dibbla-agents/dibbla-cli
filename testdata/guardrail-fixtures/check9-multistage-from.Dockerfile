# Check 9 fixture — MUST NOT trip the check (PASS).
#
# `dist` appears twice, and neither occurrence is a build-context read:
#   - `RUN npm run build` produces /src/dist INSIDE the build stage.
#   - `COPY --from=build` reads it from that stage, not from the upload archive.
# Precision rule 1 exempts `COPY --from=`. Precision rule 2 means the bare
# appearance of the string `dist` on a line is not what the check matches.
FROM node:20 AS build
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 80
