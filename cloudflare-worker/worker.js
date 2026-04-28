// Cloudflare Worker that relays Twitch's PlaybackAccessToken (GQL) and
// Client-Integrity requests so they originate from a Cloudflare datacenter
// rather than the user's residential IP. Twitch's mid-roll ad stitching is
// driven by the requesting IP at token-issuance time; routing those two
// endpoints through Cloudflare's egress suppresses most stitched ads
// (ttv-lol-pro / luminous use the same approach).
//
// Deploy:
//   1. Sign in at https://dash.cloudflare.com — Workers free tier is enough
//      (100,000 req/day, 10ms CPU/req; one viewing session uses < 50 req).
//   2. Workers & Pages → Create → Hello World. Paste this file. Save & Deploy.
//   3. Copy the *.workers.dev URL it gives you.
//   4. In ~/.config/twui/config.toml:
//        [twitch]
//        proxy-url = "https://<your-worker>.workers.dev"
//      …or pass --twitch-proxy-url=… on the command line.
//
// Security: this worker has no auth. The .workers.dev URL is unguessable but
// not secret. For private use that's fine; if you want a shared-secret header
// check, look at PROXY_SECRET below — set it as a Worker secret and add a
// matching header in twui (twitch.proxy-headers, future addition).

const ROUTES = {
	'/gql': 'https://gql.twitch.tv/gql',
	'/integrity': 'https://passport.twitch.tv/integrity',
};

// Headers Cloudflare adds to inbound requests; stripping them keeps the
// upstream request looking like it came from the worker, not the original
// client, and avoids leaking the user's IP/country to Twitch.
const STRIP_REQUEST_HEADERS = [
	'host',
	'cf-connecting-ip',
	'cf-ipcountry',
	'cf-ray',
	'cf-visitor',
	'cf-worker',
	'x-forwarded-for',
	'x-forwarded-proto',
	'x-real-ip',
];

export default {
	async fetch(request, env) {
		const url = new URL(request.url);
		const target = ROUTES[url.pathname];
		if (!target) {
			return new Response('not found', { status: 404 });
		}

		// Optional shared secret. Set with `wrangler secret put PROXY_SECRET`
		// or in the dashboard under Settings → Variables → Secrets, then add
		// the matching X-Proxy-Secret header on the client side.
		if (env.PROXY_SECRET) {
			if (request.headers.get('x-proxy-secret') !== env.PROXY_SECRET) {
				return new Response('forbidden', { status: 403 });
			}
		}

		const headers = new Headers(request.headers);
		for (const h of STRIP_REQUEST_HEADERS) headers.delete(h);
		headers.delete('x-proxy-secret');

		const init = {
			method: request.method,
			headers,
			body:
				request.method === 'GET' || request.method === 'HEAD'
					? undefined
					: await request.arrayBuffer(),
		};

		const upstream = await fetch(target, init);
		return new Response(upstream.body, {
			status: upstream.status,
			statusText: upstream.statusText,
			headers: upstream.headers,
		});
	},
};
