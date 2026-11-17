// ncrust MODE URL...: reqwest with rustls (root added from PEM), rustls (rustls-native-certs reading SSL_CERT_FILE), or native-tls (OpenSSL on Linux).
use std::time::Duration;
fn main() {
    let a: Vec<String> = std::env::args().collect();
    let mode = a[1].as_str();
    let pem = std::fs::read("/c2/bundle.pem").unwrap();
    let certs = reqwest::Certificate::from_pem_bundle(&pem).unwrap();
    let mut b = reqwest::blocking::Client::builder().timeout(Duration::from_secs(10));
    b = match mode {
        "rustls" => { let mut b = b.use_rustls_tls().tls_built_in_root_certs(false); for c in certs { b = b.add_root_certificate(c); } b }
        "rustls-native-roots" => b.use_rustls_tls(),
        _ => panic!("mode"),
    };
    let cl = b.build().unwrap();
    for u in &a[2..] {
        match cl.get(u).send() {
            Ok(r) => println!("{} OK {} {:?}", u, r.status().as_u16(), r.version()),
            Err(e) => { let mut s = format!("{}", e); let mut src: Option<&dyn std::error::Error> = std::error::Error::source(&e); while let Some(x) = src { s = format!("{}: {}", s, x); src = x.source(); } println!("{} REFUSED {}", u, s) }
        }
    }
}
