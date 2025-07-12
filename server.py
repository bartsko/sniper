### server.py

```python
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from datetime import datetime, timedelta
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.jobstores.sqlalchemy import SQLAlchemyJobStore
import pytz
import uuid
import json

app = FastAPI()

# Configure persistent job store to survive restarts
jobstores = {
    'default': SQLAlchemyJobStore(url='sqlite:///jobs.sqlite')
}
scheduler = BackgroundScheduler(jobstores=jobstores, timezone=pytz.UTC)
scheduler.start()

class ListingIn(BaseModel):
    exchange: str
    api_key: str
    api_secret: str
    symbol: str
    quote_amount: float
    price_markup_pct: int
    listing_time: datetime  # ISO format, Z or offset


def start_bot(listing_id: str):
    # Your bot-start logic here
    print(f"🚀 Starting bot for listing {listing_id}")


def schedule_bot_job(listing_id: str, listing_time: datetime):
    run_at = listing_time.astimezone(pytz.UTC) - timedelta(seconds=10)
    now = datetime.now(pytz.UTC)
    if run_at <= now:
        return  # too late
    job_id = f"bot-for-{listing_id}"
    scheduler.add_job(
        start_bot,
        'date',
        run_date=run_at,
        args=[listing_id],
        id=job_id,
        replace_existing=True
    )

@app.post("/add_listing")
def add_listing(payload: ListingIn):
    # Append to listings.json
    try:
        with open("listings.json", "r+") as f:
            data = json.load(f)
    except FileNotFoundError:
        data = []
    new_id = str(uuid.uuid4())
    entry = {
        "id": new_id,
        "exchange": payload.exchange,
        "api_key": payload.api_key,
        "api_secret": payload.api_secret,
        "symbol": payload.symbol,
        "quote_amount": payload.quote_amount,
        "price_markup_pct": payload.price_markup_pct,
        "listing_time": payload.listing_time.isoformat()
    }
    data.append(entry)
    with open("listings.json", "w") as f:
        json.dump(data, f, indent=2)

    # Schedule bot job
    schedule_bot_job(new_id, payload.listing_time)

    return {"status": "ok", "id": new_id}
```

---

### Updated AddListingView\.swift

```swift
import SwiftUI

// MARK: – Date to UTC conversion
extension Date {
    /// Returns a new Date in UTC from the current Date
    func toUTC() -> Date {
        let seconds = -TimeInterval(TimeZone.current.secondsFromGMT(for: self))
        return addingTimeInterval(seconds)
    }
}

// MARK: – HTTP POST helper
func sendListingToVPS(_ listing: [String: Any], vpsIP: String, completion: @escaping (Bool) -> Void) {
    guard let url = URL(string: "http://\(vpsIP):8000/add_listing") else {
        completion(false); return
    }
    var request = URLRequest(url: url)
    request.httpMethod = "POST"
    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
    request.httpBody = try? JSONSerialization.data(withJSONObject: listing)

    URLSession.shared.dataTask(with: request) { data, _, _ in
        guard
            let data = data,
            let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
            json["status"] as? String == "ok"
        else {
            completion(false)
            return
        }
        completion(true)
    }.resume()
}

enum Exchange: String, CaseIterable, Identifiable {
    case mexc, binance, bitget
    var id: String { rawValue }
    var displayName: String {
        switch self {
        case .mexc:   return "MEXC"
        case .binance:return "Binance"
        case .bitget: return "Bitget"
        }
    }
    var logoName: String {
        rawValue + "_logo"
    }
}

struct AddListingView: View {
    @EnvironmentObject private var store: ListingStore
    @Environment(\.dismiss) private var dismiss
    @AppStorage("vpsIP") private var vpsIP: String = ""

    @State private var selectedExchange: Exchange? = nil
    @State private var apiKey: String = ""
    @State private var apiSecret: String = ""
    @State private var listingDate = Date()
    @State private var listingSeconds = 0
    @State private var symbol = ""
    @State private var amount = ""
    @State private var tpPercent = ""
    @State private var isLoading = false

    private var isoFormatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        f.timeZone = TimeZone(secondsFromGMT: 0)
        return f
    }()

    private var combinedDate: Date {
        var comps = Calendar.current.dateComponents([.year, .month, .day, .hour, .minute], from: listingDate)
        comps.second = listingSeconds
        return Calendar.current.date(from: comps) ?? listingDate
    }

    var body: some View {
        Form {
            Section("VPS IP") {
                TextField("IP VPS", text: $vpsIP)
                    .autocapitalization(.none)
                    .textFieldStyle(.roundedBorder)
            }

            Section("Wybierz giełdę & API") {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 16) {
                        ForEach(Exchange.allCases) { ex in
                            Button {
                                selectedExchange = ex
                                apiKey = ""
                                apiSecret = ""
                            } label: {
                                VStack {
                                    Image(ex.logoName)
                                        .resizable()
                                        .scaledToFit()
                                        .frame(width: 40, height: 40)
                                    Text(ex.displayName)
                                        .font(.caption)
                                }
                                .padding(8)
                                .background(selectedExchange == ex ? Color.blue.opacity(0.2) : Color.gray.opacity(0.1))
                                .cornerRadius(8)
                            }
                        }
                    }
                    .padding(.vertical, 4)

                    if selectedExchange != nil {
                        TextField("API Key", text: $apiKey)
                            .textFieldStyle(.roundedBorder)
                            .autocapitalization(.none)
                        SecureField("API Secret", text: $apiSecret)
                            .textFieldStyle(.roundedBorder)
                    }
                }
            }

            if selectedExchange != nil {
                Section("Nowy Listing") {
                    HStack {
                        Text("Data i czas lokalne")
                        Spacer()
                        Text(isoFormatter.string(from: combinedDate.toUTC()))
                            .foregroundColor(.secondary)
                    }
                    DatePicker("Data", selection: $listingDate, displayedComponents: .date)
                        .datePickerStyle(.compact)
                    DatePicker("Godzina", selection: $listingDate, displayedComponents: .hourAndMinute)
                        .datePickerStyle(.compact)
                    HStack {
                        Text("Sekundy")
                        Spacer()
                        Stepper("\(listingSeconds)", value: $listingSeconds, in: 0...59)
                            .labelsHidden()
                    }

                    TextField("Symbol", text: $symbol)
                        .autocapitalization(.allCharacters)
                        .textFieldStyle(.roundedBorder)
                    TextField("Kwota", text: $amount)
                        .keyboardType(.decimalPad)
                        .textFieldStyle(.roundedBorder)
                    HStack {
                        TextField("Take profit (%)", text: $tpPercent)
                            .keyboardType(.numberPad)
                            .textFieldStyle(.roundedBorder)
                        Text("%")
                    }
                }
            }

            Section {
                Button(action: addListing) {
                    if isLoading {
                        ProgressView()
                    } else {
                        Text("Dodaj Listing")
                    }
                }
                .disabled(vpsIP.isEmpty || selectedExchange == nil || apiKey.isEmpty || apiSecret.isEmpty || symbol.isEmpty)
                .buttonStyle(.borderedProminent)
            }
        }
        .navigationTitle("Dodaj Listing")
        .navigationBarTitleDisplayMode(.inline)
    }

    private func addListing() {
        guard let ex = selectedExchange else { return }
        isLoading = true
        let utcDate = combinedDate.toUTC()
        let iso = isoFormatter.string(from: utcDate)
        let payload: [String: Any] = [
            "exchange": ex.rawValue,
            "api_key": apiKey,
            "api_secret": apiSecret,
            "symbol": symbol.uppercased(),
            "quote_amount": Double(amount) ?? 0,
            "price_markup_pct": Int(tpPercent) ?? 0,
            "listing_time": iso
        ]
        sendListingToVPS(payload, vpsIP: vpsIP) { success in
            DispatchQueue.main.async {
                isLoading = false
                if success {
                    store.fetchListings()
                    dismiss()
                }
            }
        }
    }
}
```
