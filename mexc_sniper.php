<?php
// mexc_ultra.php — ultraszybki bot MEXC Market Buy + Latency log + TLS warmup + logfile

$logfile = __DIR__ . '/mexc_bot.log';
function logit($msg) {
    global $logfile;
    file_put_contents($logfile, "[" . date('Y-m-d H:i:s') . "] $msg\n", FILE_APPEND);
}

$listing = json_decode(file_get_contents(__DIR__ . '/current_listing.json'), true);
if (!$listing) {
    logit("❌ Brak current_listing.json");
    die("❌ Brak current_listing.json\n");
}

$api_key     = $listing['api_key'];
$api_secret  = $listing['api_secret'];
$symbol      = strtoupper($listing['symbol']);
$quote_amount= floatval($listing['quote_amount']);
$profit_pct  = floatval($listing['profit_pct'] ?? 10);

// HMAC SHA256 podpis
function sign($params, $secret) {
    ksort($params);
    $query = http_build_query($params, '', '&', PHP_QUERY_RFC3986);
    return hash_hmac('sha256', $query, $secret);
}

// TLS WARMUP (HEAD request) — rozgrzewa sesję HTTPS!
function tls_warmup($api_key) {
    $ch = curl_init('https://api.mexc.com/api/v3/time');
    curl_setopt($ch, CURLOPT_HTTPHEADER, ['X-MEXC-APIKEY: '.$api_key]);
    curl_setopt($ch, CURLOPT_NOBODY, true);
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_exec($ch);
    curl_close($ch);
}

// Pomiar czasu w mikrosekundach
function mtime() {
    return microtime(true);
}

// Rozgrzewka TLS
tls_warmup($api_key);

// MARKET BUY
$url = 'https://api.mexc.com/api/v3/order';
$params = [
    'symbol'        => $symbol,
    'side'          => 'BUY',
    'type'          => 'MARKET',
    'quoteOrderQty' => $quote_amount,
    'timestamp'     => round(microtime(true)*1000),
    'recvWindow'    => 5000
];
$params['signature'] = sign($params, $api_secret);

$ch = curl_init($url . '?' . http_build_query($params, '', '&', PHP_QUERY_RFC3986));
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'X-MEXC-APIKEY: ' . $api_key,
    'Content-Type: application/json'
]);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POSTFIELDS, '');

$start = mtime();
$resp = curl_exec($ch);
$latency = round((mtime() - $start) * 1000); // ms
$httpcode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
curl_close($ch);

// Logujemy wszystko
logit("MARKET BUY sent: " . json_encode($params));
logit("HTTP $httpcode, latency: {$latency} ms, resp: $resp");

echo "⏱ Latency: {$latency} ms\n";

if ($httpcode != 200) {
    logit("❌ MARKET BUY FAIL, HTTP $httpcode, RESP: $resp");
    die("❌ MARKET BUY fail $resp\n");
}

$data = json_decode($resp, true);
if (isset($data['orderId'])) {
    echo "🎉 MARKET BUY ok: orderId=" . $data['orderId'] . "\n";
    logit("🎉 MARKET BUY ok: orderId=" . $data['orderId']);
} else {
    logit("❌ MARKET BUY error: " . $resp);
    echo "❌ MARKET BUY error: $resp\n";
}

// Done!
?>
