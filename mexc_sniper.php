<?php
// mexc_sniper.php — ultraszybki PHP bot na MEXC (market buy + take profit)

$listing = json_decode(file_get_contents(__DIR__ . '/current_listing.json'), true);
if (!$listing) die("❌ Nie mogę odczytać current_listing.json\n");

$api_key     = $listing['api_key'];
$api_secret  = $listing['api_secret'];
$symbol      = strtoupper($listing['symbol']);
$quote_amount= floatval($listing['quote_amount']);
$profit_pct  = floatval($listing['profit_pct'] ?? 15);

// HMAC SHA256
function sign($params, $secret) {
    ksort($params);
    $query = http_build_query($params, '', '&', PHP_QUERY_RFC3986);
    return hash_hmac('sha256', $query, $secret);
}

// --- CURL Init, re-use handle!
$ch = curl_init();
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_HTTPHEADER, [
    'Connection: keep-alive',
    'Content-Type: application/json',
]);

// MARKET BUY
function market_buy($ch, $symbol, $quote_amount, $api_key, $api_secret) {
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

    $headers = [
        'X-MEXC-APIKEY: ' . $api_key,
        'Connection: keep-alive',
        'Content-Type: application/json',
        'Expect:',
    ];

    curl_setopt($ch, CURLOPT_URL, $url . '?' . http_build_query($params));
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, $headers);
    curl_setopt($ch, CURLOPT_POSTFIELDS, '');
    $send = microtime(true);
    $resp = curl_exec($ch);
    $recv = microtime(true);
    $httpcode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    if ($httpcode != 200) die("❌ MARKET BUY fail $resp\n");
    $data = json_decode($resp, true);
    $latency = round(($recv - $send) * 1000);
    printf("🎉 MARKET BUY ok: orderId=%s\n%-24s | %-24s | %9d | %s\n",
        $data['orderId'],
        date('H:i:s.u', (int)$send) . sprintf("%03d", ($send*1000)%1000),
        date('H:i:s.u', (int)$recv) . sprintf("%03d", ($recv*1000)%1000),
        $latency,
        'MARKET BUY'
    );
    return [$data['orderId'], $latency];
}

// LIMIT SELL (TP)
function limit_sell($ch, $symbol, $qty, $price, $api_key, $api_secret) {
    $url = 'https://api.mexc.com/api/v3/order';
    $params = [
        'symbol'    => $symbol,
        'side'      => 'SELL',
        'type'      => 'LIMIT',
        'timeInForce'=> 'GTC',
        'quantity'  => sprintf('%.8f', $qty),
        'price'     => sprintf('%.8f', $price),
        'timestamp' => round(microtime(true)*1000),
        'recvWindow'=> 5000
    ];
    $params['signature'] = sign($params, $api_secret);

    $headers = [
        'X-MEXC-APIKEY: ' . $api_key,
        'Connection: keep-alive',
        'Content-Type: application/json',
        'Expect:',
    ];

    curl_setopt($ch, CURLOPT_URL, $url . '?' . http_build_query($params));
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_HTTPHEADER, $headers);
    curl_setopt($ch, CURLOPT_POSTFIELDS, '');
    $send = microtime(true);
    $resp = curl_exec($ch);
    $recv = microtime(true);
    $httpcode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    if ($httpcode != 200) die("❌ LIMIT SELL fail $resp\n");
    $data = json_decode($resp, true);
    $latency = round(($recv - $send) * 1000);
    printf("🎯 TP SELL: orderId=%s\n%-24s | %-24s | %9d | %s\n",
        $data['orderId'],
        date('H:i:s.u', (int)$send) . sprintf("%03d", ($send*1000)%1000),
        date('H:i:s.u', (int)$recv) . sprintf("%03d", ($recv*1000)%1000),
        $latency,
        'LIMIT SELL'
    );
    return $latency;
}

// -- GŁÓWNA LOGIKA
echo "📊 Performance:\n";
echo "Sent                    | Received                | Latency   | Type  \n";

list($orderId, $lat1) = market_buy($ch, $symbol, $quote_amount, $api_key, $api_secret);
// Tu możesz dodać fetch_order jeśli chcesz (opcjonalnie: sleep(0.5);)

$exec_price = $listing['price'] ?? 0; // Jeśli masz order fetch — pobierz real price!
if (!$exec_price) $exec_price = $listing['limit_price'] ?? 0; // fallback jeśli brak

if (!$exec_price) {
    // Możesz dodać fetch_order() w razie potrzeby, a tu sleep(1)
    sleep(1);
    // ... pobierz order fetch tu i nadpisz $exec_price
}

$tp_price = $exec_price ? $exec_price * (1.0 + $profit_pct/100.0) : 0;
$qty = $listing['qty'] ?? 0; // lub pobierz z fetch_order
if ($tp_price && $qty) {
    $lat2 = limit_sell($ch, $symbol, $qty, $tp_price, $api_key, $api_secret);
}

curl_close($ch);

echo "✅ Zakończono\n";
