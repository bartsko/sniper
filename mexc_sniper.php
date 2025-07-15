<?php
// mexc_sniper.php — superszybki bot PHP do market buy na MEXC, bierze dane z current_listing.json

// === 1. Wczytaj dane z current_listing.json ===
$listing_file = __DIR__ . '/current_listing.json';
if (!file_exists($listing_file)) {
    fwrite(STDERR, "Brak pliku current_listing.json\n");
    exit(1);
}
$listing = json_decode(file_get_contents($listing_file), true);
$api_key     = $listing['api_key'];
$api_secret  = $listing['api_secret'];
$symbol      = strtoupper($listing['symbol']);
$quote_amt   = floatval($listing['quote_amount']);
$profit_pct  = floatval($listing['profit_pct'] ?? 15);

// === 2. Funkcja podpisująca ===
function sign($params, $secret) {
    ksort($params);
    $query = http_build_query($params, '', '&', PHP_QUERY_RFC3986);
    return hash_hmac('sha256', $query, $secret);
}

// === 3. Pobierz czas serwera z MEXC ===
function get_server_time() {
    $data = json_decode(file_get_contents('https://api.mexc.com/api/v3/time'), true);
    return $data['serverTime'];
}

// === 4. Market Buy + TP z tabelą opóźnień ===
function ms_time_format($ms) {
    $dt = new DateTime("@".intval($ms/1000));
    $dt->setTimezone(new DateTimeZone(date_default_timezone_get()));
    $milli = str_pad($ms % 1000, 3, '0', STR_PAD_LEFT);
    return $dt->format("H:i:s") . ".$milli";
}

echo "📊 Performance:\n";
echo str_pad("Sent", 24) . " | " . str_pad("Received", 24) . " | " . str_pad("Latency", 8) . " | Type\n";

// ——— MARKET BUY ———
$server_time = get_server_time();
$params = [
    'symbol'        => $symbol,
    'side'          => 'BUY',
    'type'          => 'MARKET',
    'quoteOrderQty' => $quote_amt,
    'timestamp'     => $server_time,
    'recvWindow'    => 5000
];
$params['signature'] = sign($params, $api_secret);
$url = 'https://api.mexc.com/api/v3/order?' . http_build_query($params, '', '&', PHP_QUERY_RFC3986);

$headers = [
    'X-MEXC-APIKEY: ' . $api_key,
    'Content-Type: application/json'
];
$sent = microtime(true);
$ch = curl_init();
curl_setopt($ch, CURLOPT_URL, $url);
curl_setopt($ch, CURLOPT_HTTPHEADER, $headers);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POSTFIELDS, '');
$resp = curl_exec($ch);
$received = microtime(true);
curl_close($ch);

$latency = intval(($received - $sent) * 1000);
$data = json_decode($resp, true);
if (isset($data['orderId'])) {
    echo "🎉 MARKET BUY ok: orderId={$data['orderId']}\n";
    echo ms_time_format(intval($sent*1000)) . " | " . ms_time_format(intval($received*1000)) . " | ";
    echo str_pad($latency, 8) . " | MARKET BUY\n";
    $orderId = $data['orderId'];
} else {
    echo "❌ MARKET BUY fail $resp\n";
    exit(1);
}

// ——— TP SELL ———
sleep(1);
$params = [
    'symbol'    => $symbol,
    'orderId'   => $orderId,
    'timestamp' => get_server_time(),
    'recvWindow'=> 5000
];
$params['signature'] = sign($params, $api_secret);
$url = 'https://api.mexc.com/api/v3/order?' . http_build_query($params, '', '&', PHP_QUERY_RFC3986);
$resp = file_get_contents($url, false, stream_context_create(['http' => ['header' => "X-MEXC-APIKEY: $api_key\r\n"]]));
$order_data = json_decode($resp, true);
$qty   = floatval($order_data['executedQty']);
$price = floatval($order_data['price']);
$tp_price = round($price * (1.0 + $profit_pct/100.0), 8);

$params = [
    'symbol'    => $symbol,
    'side'      => 'SELL',
    'type'      => 'LIMIT',
    'timeInForce'=> 'GTC',
    'quantity'  => sprintf('%.8f', $qty),
    'price'     => sprintf('%.8f', $tp_price),
    'timestamp' => get_server_time(),
    'recvWindow'=> 5000
];
$params['signature'] = sign($params, $api_secret);
$url = 'https://api.mexc.com/api/v3/order?' . http_build_query($params, '', '&', PHP_QUERY_RFC3986);

$sent2 = microtime(true);
$ch = curl_init();
curl_setopt($ch, CURLOPT_URL, $url);
curl_setopt($ch, CURLOPT_HTTPHEADER, $headers);
curl_setopt($ch, CURLOPT_POST, true);
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_POSTFIELDS, '');
$resp2 = curl_exec($ch);
$received2 = microtime(true);
curl_close($ch);

$latency2 = intval(($received2 - $sent2) * 1000);
$data2 = json_decode($resp2, true);
if (isset($data2['orderId'])) {
    echo "🎯 TP SELL: orderId={$data2['orderId']}\n";
    echo ms_time_format(intval($sent2*1000)) . " | " . ms_time_format(intval($received2*1000)) . " | ";
    echo str_pad($latency2, 8) . " | LIMIT SELL\n";
} else {
    echo "❌ TP SELL fail $resp2\n";
    exit(1);
}

echo "✅ Zakończono\n";
