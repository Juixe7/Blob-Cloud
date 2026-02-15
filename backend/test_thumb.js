async function test() {
  try {
    const res = await fetch('http://localhost:8090/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        email: 'preserve@example.com',
        password: 'Password123!'
      })
    });
    const data = await res.json();
    console.log("Login data:", data);
    const token = data.token;
    console.log("Got token");

    const fileId = "13243861-d431-434e-a193-dea61eedf4e5";
    const url = `http://localhost:8090/api/files/${fileId}/thumbnail?token=${token}`;
    console.log("Fetching:", url);

    const thumbRes = await fetch(url);
    console.log("Status:", thumbRes.status);
    console.log("Content-Type:", thumbRes.headers.get('content-type'));
    const text = await thumbRes.text();
    console.log("Body excerpt:", text.substring(0, 100));
  } catch (err) {
    console.log("Error:", err.message);
  }
}

test();
