import { Container } from "@cloudflare/containers";

export interface Env {
  MISTRAL_API_KEY: string;
  INTERNAL_SHARED_SECRET: string;
  PDFPROC: PdfProcContainer;
}

class PdfProcContainer extends Container {
  defaultPort = 8080;
  envVars = {
    MISTRAL_API_KEY: "",
    INTERNAL_SHARED_SECRET: "",
  };
}

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    const url = new URL(req.url);

    if (url.pathname === "/health") {
      return new Response("ok");
    }

    if (url.pathname === "/api/pdf/extract" && req.method === "POST") {
      env.PDFPROC.envVars = {
        MISTRAL_API_KEY: env.MISTRAL_API_KEY,
        INTERNAL_SHARED_SECRET: env.INTERNAL_SHARED_SECRET,
      };

      const body = await req.text();
      return env.PDFPROC.fetch(
        new Request("http://container/extract", {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "X-Internal-Auth": env.INTERNAL_SHARED_SECRET,
          },
          body,
        })
      );
    }

    if (url.pathname === "/api/pdf/preview" && req.method === "POST") {
      env.PDFPROC.envVars = {
        MISTRAL_API_KEY: env.MISTRAL_API_KEY,
        INTERNAL_SHARED_SECRET: env.INTERNAL_SHARED_SECRET,
      };

      const body = await req.text();
      return env.PDFPROC.fetch(
        new Request("http://container/preview", {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "X-Internal-Auth": env.INTERNAL_SHARED_SECRET,
          },
          body,
        })
      );
    }

    return new Response("not found", { status: 404 });
  },
};
