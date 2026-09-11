# ---- build stage ----
FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/hass-agent-bot ./cmd/bot

# ---- runtime stage ----
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

# Сертификат Минцифры (нужен для GigaChat API: ngw.devices.sberbank.ru:9443)
COPY russian_trusted_root_ca_pem.crt /usr/local/share/ca-certificates/russian_trusted_root_ca_pem.crt
RUN update-ca-certificates

COPY --from=build /out/hass-agent-bot /hass-agent-bot

# Системный промпт (редактируется в data/system_prompt.txt в репозитории).
# Кладём в /system_prompt.txt (корень контейнера) как фоллбэк: если volume /data
# смонтирован целиком и перекрывает встроенную копию, бот подхватит файл отсюда.
COPY data/system_prompt.txt /system_prompt.txt

ENV TZ=Europe/Moscow

VOLUME ["/data"]

ENTRYPOINT ["/hass-agent-bot"]