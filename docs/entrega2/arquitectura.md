# Arquitectura desplegada (Entrega 2)

Diagramas de la solución tal como quedó desplegada en Google Cloud. El código fuente de cada diagrama es el bloque Mermaid, que se renderiza directamente en GitHub y en cualquier visor compatible.

## Componentes

Diagrama de componentes UML. Las interfaces provistas se dibujan como lollipops junto al componente que las ofrece, y cada flecha es una dependencia con el nombre de la interfaz que consume. El código fuente es `diagramas/componentes.puml` y se regenera con PlantUML.

![Modelo de componentes](diagramas/componentes.svg)

La API es un monolito modular en Go. Los módulos son `auth`, `admin`, `courses`, `enroll`, `quiz`, `progress`, `badges`, `uploads`, `storage`, `mail`, `queue` y `telemetry`. El frontend Next.js se reutiliza de la entrega anterior y Caddy lo sirve junto con la API bajo el mismo origen, lo que evita configurar CORS.

Las comunicaciones síncronas son HTTP entre el navegador y Caddy, y HTTP interno entre Caddy y la API o el frontend. El worker habla con PostgreSQL y con Cloud Storage. Las asíncronas pasan por Redis con asynq, y son las dos únicas tareas de la aplicación, `media:scan` y `media:transcode`.

## Despliegue

```mermaid
flowchart TB
  INTERNET(("Internet"))

  subgraph gcp["Google Cloud · proyecto proyecto-2-cloud-509714 · us-central1-a"]
    subgraph vpc["VPC mooc-vpc · subred mooc-app-subnet 10.10.1.0/24"]
      FW_WEB["Firewall mooc-allow-web<br/>0.0.0.0/0 → 80,443"]
      FW_INT["Firewall mooc-allow-internal<br/>10.10.1.0/24 → todo"]
      FW_IAP["Firewall mooc-allow-iap-ssh<br/>35.235.240.0/20 → 22"]

      subgraph vmweb["VM mooc-web · e2-small · 2 vCPU 1976 MB · 30 GiB pd-balanced"]
        C1["caddy"]
        C2["frontend"]
        C3["backend"]
        C4["mailpit"]
      end

      subgraph vmworker["VM mooc-worker · e2-small · sin IP pública"]
        C5["redis"]
        C6["worker"]
        C7["clamav"]
      end

      subgraph vmload["VM mooc-loadgen · e2-small · sin IP pública"]
        K6["k6 v2.3.0"]
      end

      NAT["Cloud NAT mooc-router-nat"]
      IAP["Identity-Aware Proxy<br/>túnel SSH"]
    end

    SQL[("Cloud SQL mooc-postgres<br/>PostgreSQL 17 · db-f1-micro · 20 GiB<br/>solo IP privada 10.179.0.3")]
    B1[("Bucket originals")]
    B2[("Bucket hls")]
    B3[("Bucket public")]
    SM["Secret Manager"]
  end

  INTERNET -->|"443 HTTPS"| FW_WEB
  FW_WEB --> vmweb
  INTERNET -->|"SSH por túnel"| IAP
  IAP --> FW_IAP
  FW_IAP --> vmweb
  FW_IAP --> vmworker
  vmweb <-->|"privado"| FW_INT
  vmworker <-->|"privado"| FW_INT
  FW_INT --> SQL
  vmload -.->|"egreso a Internet"| NAT
  vmweb -.->|"egreso a Internet"| NAT
  vmworker -.->|"egreso a Internet"| NAT
  vmworker --> B1
  vmworker --> B2
  vmweb --> B1
  vmweb --> B3
  vmweb -->|"credenciales"| SM
  vmworker -->|"credenciales"| SM
```

La IP pública la tiene solo el Web Server, que es el punto de entrada. El Worker Server y el generador no tienen IP pública y se administran por SSH a través de IAP. Cloud SQL no acepta conexiones desde Internet porque tiene la IPv4 deshabilitada y solo expone la IP privada. Las tres máquinas salen a Internet por Cloud NAT para instalar dependencias. Los volúmenes persistentes son los discos de arranque de cada VM, con `redis_data` como volumen Docker para el `appendonly` de Redis.

## Carga directa de un archivo

```mermaid
sequenceDiagram
  autonumber
  participant P as Profesor (navegador)
  participant A as API Go
  participant R as Redis (asynq)
  participant G as Cloud Storage originals
  participant W as Worker Go
  participant C as ClamAV
  participant F as ffmpeg

  P->>A: POST /api/v1/resources/{id}/upload-url
  Note over A,R: La API autoriza y encola el escaneo<br/>en la misma petición
  A->>R: enqueue media:scan
  A-->>P: URL prefirmada
  R-->>W: toma media:scan
  W->>G: GET objeto
  Note over W,G: El objeto todavía no existe:<br/>el cliente aún no ha subido nada.<br/>El primer intento falla y asynq reintenta a los 30 s
  P->>G: PUT del archivo (transferencia directa)
  R-->>W: reintento de media:scan
  W->>G: GET objeto
  G-->>W: bytes del original
  W->>C: escanear
  C-->>W: limpio
  W->>R: enqueue media:transcode
  R-->>W: toma media:transcode
  W->>F: transcodificar a HLS
  F-->>W: manifiesto y segmentos
  W->>G: PUT derivados al bucket hls
  W->>A: processing_status = ready
  Note over W,A: La API refresca el estado en PostgreSQL
```

Este es el flujo que el informe documenta con una condición de carrera. El encolado del escaneo ocurre antes de que el objeto exista, así que el primer intento falla por clave inexistente y el trabajo se resuelve en el reintento. La corrección consiste en separar la autorización de la confirmación, con un endpoint de confirmación que encole el escaneo una vez terminada la subida.

## Consumo de contenido

```mermaid
sequenceDiagram
  autonumber
  participant E as Estudiante (navegador)
  participant A as API Go
  participant G as Cloud Storage hls

  E->>A: GET /api/v1/resources/{id}/download-url
  A-->>E: URL firmada del manifiesto
  E->>G: GET index.m3u8
  G-->>E: manifiesto
  loop Un segmento por intervalo de reproducción
    E->>G: GET seg00N.ts
    G-->>E: segmento
  end
```

El contenido multimedia no pasa por la API. El navegador lo pide directo a Cloud Storage con una URL firmada, y el tráfico de control contra la API se limita a autorizar la descarga.

## Ciclo de vida del recurso

```mermaid
stateDiagram-v2
  [*] --> pending: se crea el recurso
  pending --> processing: el worker toma media:scan
  processing --> processing: reintento tras fallo de escaneo
  processing --> ready: escaneo limpio y HLS generado
  processing --> failed: agota reintentos
  failed --> [*]: queda diagnosticable en la cola
  ready --> [*]: disponible para el estudiante
```

Un recurso en `failed` no se publica. El sistema lo conserva en lugar de perderlo, que es lo que el enunciado pide verificar en el drenaje.

## Decisiones y adaptaciones

```mermaid
flowchart LR
  subgraph entrega1["Entrega anterior"]
    E1["Redis y asynq<br/>contenedor local"]
    E2["MinIO<br/>almacenamiento local"]
    E3["PostgreSQL<br/>contenedor local"]
  end
  subgraph entrega2["Entrega 2"]
    F1["Redis y asynq<br/>contenedor en Worker Server<br/>exigido por el enunciado"]
    F2["Cloud Storage<br/>tres buckets + interoperabilidad S3"]
    F3["Cloud SQL PostgreSQL 17<br/>IP privada"]
  end
  E1 --> F1
  E2 --> F2
  E3 --> F3
```

Los cambios frente a la entrega anterior son tres. Redis se mantiene en contenedor y se mueve al Worker Server, porque el enunciado exige que el sistema de mensajería viva en un contenedor de esa máquina. El almacenamiento local se reemplaza por Cloud Storage, conservando la organización lógica de los objetos y los flujos de carga directa y URL firmada. PostgreSQL sale del contenedor y pasa a Cloud SQL, con la conexión restringida a la IP privada.

Del modelo de despliegue básico del enunciado quedan fuera el escalado automático, el balanceador de carga, las réplicas entre máquinas y la alta disponibilidad. La configuración de cómputo es fija durante las corridas de capacidad, que es lo que permite comparar niveles entre sí.
