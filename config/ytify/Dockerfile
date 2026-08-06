FROM node:22-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
ARG YTIFY_COMMIT
RUN git clone https://github.com/n-ce/ytify.git . && git checkout "$YTIFY_COMMIT"
RUN npm install && npm run build

FROM nginx:1.27-alpine
COPY --from=builder /src/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
