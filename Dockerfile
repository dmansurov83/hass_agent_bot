# ---- build stage ----
FROM golang:1.27-alpine AS build

WORKDIR /src

# Кэшируем зависимости отдельно
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Статическая сборка без CGO для чистого scratch-образа
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/hass-agent-bot ./cmd/bot

# ---- runtime stage ----
FROM scratch

COPY --from=build /out/hass-agent-bot /hass-agent-bot

ENV TZ=Europe/Moscow

VOLUME ["/data"]

ENTRYPOINT ["/hass-agent-bot"]