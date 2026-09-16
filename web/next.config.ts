import type { NextConfig } from "next";

const apiOrigin = process.env.RECORD_HUB_API_ORIGIN ?? "http://127.0.0.1:8080";

const nextConfig: NextConfig = {
  async rewrites() {
    return [
      { source: "/api/:path*", destination: `${apiOrigin}/api/:path*` },
      { source: "/auth/:path*", destination: `${apiOrigin}/auth/:path*` },
    ];
  },
};

export default nextConfig;
