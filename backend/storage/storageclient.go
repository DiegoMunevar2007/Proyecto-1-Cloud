package storage

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
)

// Buckets lógicos (misma instancia MinIO, prefijos/buckets separados).
const (
	BucketOriginals = "originals"
	BucketHLS       = "hls"
	BucketPublic    = "public"
)

// BadgeImageKey es la insignia estática por defecto (bucket público).
const BadgeImageKey = "badges/default.png"

// TusMetaPrefix segrega los sidecars de tusd (.info/.part): el lifecycle
// del bucket los expira y los objetos finales quedan limpios.
const TusMetaPrefix = "tus-meta/"

//go:embed static/badge-default.png
var defaultBadgePNG []byte

// Client envuelve MinIO/S3 con URLs prefirmadas.
type Client struct {
	mc         *minio.Client
	presign    *minio.Client
	cdnBase    string
	presignTTL time.Duration
}

// NewClient crea el cliente desde variables de entorno y asegura buckets.
func NewClient() (*Client, error) {
	endpoint := utils.GetEnv("S3_ENDPOINT", "localhost:9000")
	user := utils.GetEnv("S3_ACCESS_KEY", "minioadmin")
	pass := utils.GetEnv("S3_SECRET_KEY", "minioadmin")
	useSSL := utils.GetEnv("S3_USE_SSL", "false") == "true"
	region := utils.GetEnv("S3_REGION", "us-east-1")
	cdn := strings.TrimSuffix(utils.GetEnv("CDN_BASE_URL", ""), "/")

	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(user, pass, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	// Segundo cliente solo para firmar URLs con el endpoint público:
	// SigV4 incluye el host en la firma, por lo que la URL debe firmarse
	// con el mismo host que usará el navegador (S3_PUBLIC_ENDPOINT).
	presignEndpoint := endpoint
	presignSecure := useSSL
	if pub := strings.TrimSuffix(utils.GetEnv("S3_PUBLIC_ENDPOINT", ""), "/"); pub != "" {
		if pu, perr := url.Parse(pub); perr == nil && pu.Host != "" {
			presignEndpoint = pu.Host
			presignSecure = pu.Scheme == "https"
		}
	}
	pc, err := minio.New(presignEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(user, pass, ""),
		Secure: presignSecure,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	c := &Client{mc: mc, presign: pc, cdnBase: cdn, presignTTL: 24 * time.Hour}
	ctx := context.Background()
	for _, b := range []string{BucketOriginals, BucketHLS, BucketPublic} {
		exists, err := mc.BucketExists(ctx, b)
		if err != nil {
			return nil, err
		}
		if !exists {
			if err := mc.MakeBucket(ctx, b, minio.MakeBucketOptions{Region: region}); err != nil {
				return nil, err
			}
		}
	}
	// Buckets public (thumbnails, HLS) con lectura anónima: el control de acceso
	// se verifica en la API antes de revelar la URL (rutas firmadas/UUID).
	publicRead := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/*"]}]}`
	for _, b := range []string{BucketPublic, BucketHLS} {
		_ = mc.SetBucketPolicy(ctx, b, fmt.Sprintf(publicRead, b))
	}
	// Insignia estática por defecto (idempotente: solo si falta).
	if _, err := mc.StatObject(ctx, BucketPublic, BadgeImageKey, minio.StatObjectOptions{}); err != nil {
		_, _ = mc.PutObject(ctx, BucketPublic, BadgeImageKey, bytes.NewReader(defaultBadgePNG), int64(len(defaultBadgePNG)), minio.PutObjectOptions{ContentType: "image/png"})
	}
	// Expiración de sidecars TUS (7 días): los uploads mandan, los restos no.
	lc := lifecycle.NewConfiguration()
	lc.Rules = []lifecycle.Rule{{
		ID:         "tus-meta-expiry",
		RuleFilter: lifecycle.Filter{Prefix: TusMetaPrefix},
		Status:     "Enabled",
		Expiration: lifecycle.Expiration{Days: lifecycle.ExpirationDays(7)},
	}}
	_ = mc.SetBucketLifecycle(ctx, BucketOriginals, lc)
	return c, nil
}

// PresignedPut genera una URL prefirmada de subida directa (multipart reanudable 24h).
func (c *Client) PresignedPut(bucket, objectKey string) (string, error) {
	if bucket == "" {
		bucket = BucketOriginals
	}
	u, err := c.presign.PresignedPutObject(context.Background(), bucket, objectKey, c.presignTTL)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PresignedGet genera una URL firmada de descarga tras verificar derecho de acceso.
// El controlador debe verificar rol/propiedad/inscripción antes de llamar.
func (c *Client) PresignedGet(bucket, objectKey string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > time.Hour {
		ttl = 15 * time.Minute
	}
	u, err := c.presign.PresignedGetObject(context.Background(), bucket, objectKey, ttl, url.Values{})
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PublicURL retorna la URL de distribución (CDN si está configurado, si no endpoint S3 público).
func (c *Client) PublicURL(bucket, objectKey string) string {
	if c.cdnBase != "" {
		return fmt.Sprintf("%s/%s/%s", c.cdnBase, bucket, objectKey)
	}
	return PublicObjectURL(bucket, objectKey)
}

// PublicObjectURL construye la URL pública sin cliente (para dominios
// que no deben importar el cliente, p. ej. insignias).
func PublicObjectURL(bucket, objectKey string) string {
	if cdn := strings.TrimSuffix(utils.GetEnv("CDN_BASE_URL", ""), "/"); cdn != "" {
		return fmt.Sprintf("%s/%s/%s", cdn, bucket, objectKey)
	}
	endpoint := utils.GetEnv("S3_PUBLIC_ENDPOINT", "http://localhost:9000")
	return fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(endpoint, "/"), bucket, objectKey)
}

// DownloadToFile descarga un objeto a un archivo local (uso del worker).
func (c *Client) DownloadToFile(bucket, objectKey, destPath string) error {
	return c.mc.FGetObject(context.Background(), bucket, objectKey, destPath, minio.GetObjectOptions{})
}

// DeleteFile elimina un objeto (cuarentena tras hallazgo de malware).
func (c *Client) DeleteFile(bucket, objectKey string) error {
	return c.mc.RemoveObject(context.Background(), bucket, objectKey, minio.RemoveObjectOptions{})
}

// DeletePrefix elimina todos los objetos bajo un prefijo (p. ej. hls/<stableID>/).
// Best-effort para cascadas de borrado; retorna el primer error.
func (c *Client) DeletePrefix(bucket, prefix string) error {
	ctx := context.Background()
	objectsCh := make(chan minio.ObjectInfo)
	go func() {
		defer close(objectsCh)
		for obj := range c.mc.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
			if obj.Err == nil {
				objectsCh <- obj
			}
		}
	}()
	for err := range c.mc.RemoveObjects(ctx, bucket, objectsCh, minio.RemoveObjectsOptions{}) {
		if err.Err != nil {
			return err.Err
		}
	}
	return nil
}

// UploadFile sube un archivo local (uso del worker para segmentos HLS).
func (c *Client) UploadFile(bucket, objectKey, srcPath, contentType string) error {
	_, err := c.mc.FPutObject(context.Background(), bucket, objectKey, srcPath, minio.PutObjectOptions{ContentType: contentType})
	return err
}
