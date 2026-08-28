FROM node:lts-alpine
WORKDIR /app
COPY package.json .
RUN corepack enable
RUN pnpm install
COPY . .
ARG NEXT_PUBLIC_API_URL
ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL
RUN pnpm run build
CMD ["pnpm", "run", "start"]