

CREATE TEMP FUNCTION certDecode(pem STRING)
RETURNS JSON
LANGUAGE js
AS r"""
// Base64 JavaScript decoder
// Copyright (c) 2008-2024 Lapo Luchini <lapo@lapo.it>

// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
// OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

const
    haveU8 = (typeof Uint8Array == 'function');

let decoder; // populated on first usage

class Base64 {

    static decode(a) {
        let isString = (typeof a == 'string');
        let i;
        if (decoder === undefined) {
            let b64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/',
                ignore = '= \f\n\r\t\u00A0\u2028\u2029';
            decoder = [];
            for (i = 0; i < 64; ++i)
                decoder[b64.charCodeAt(i)] = i;
            for (i = 0; i < ignore.length; ++i)
                decoder[ignore.charCodeAt(i)] = -1;
            // RFC 3548 URL & file safe encoding
            decoder['-'.charCodeAt(0)] = decoder['+'.charCodeAt(0)];
            decoder['_'.charCodeAt(0)] = decoder['/'.charCodeAt(0)];
        }
        let out = haveU8 ? new Uint8Array(a.length * 3 >> 2) : [];
        let bits = 0, char_count = 0, len = 0;
        for (i = 0; i < a.length; ++i) {
            let c = isString ? a.charCodeAt(i) : a[i];
            if (c == 61) // '='.charCodeAt(0)
                break;
            c = decoder[c];
            if (c == -1)
                continue;
            if (c === undefined)
                throw 'Illegal character at offset ' + i;
            bits |= c;
            if (++char_count >= 4) {
                out[len++] = (bits >> 16);
                out[len++] = (bits >> 8) & 0xFF;
                out[len++] = bits & 0xFF;
                bits = 0;
                char_count = 0;
            } else {
                bits <<= 6;
            }
        }
        switch (char_count) {
        case 1:
            throw 'Base64 encoding incomplete: at least 2 bits missing';
        case 2:
            out[len++] = (bits >> 10);
            break;
        case 3:
            out[len++] = (bits >> 16);
            out[len++] = (bits >> 8) & 0xFF;
            break;
        }
        if (haveU8 && out.length > len) // in case it was originally longer because of ignored characters
            out = out.subarray(0, len);
        return out;
    }

    static unarmor(a) {
        let m = Base64.re.exec(a);
        if (m) {
            if (m[1])
                a = m[1];
            else if (m[2])
                a = m[2];
            else if (m[3])
                a = m[3];
            else
                throw 'RegExp out of sync';
        }
        return Base64.decode(a);
    }

}

Base64.re = /-----BEGIN [^-]+-----([A-Za-z0-9+/=\s]+)-----END [^-]+-----|begin-base64[^\n]+\n([A-Za-z0-9+/=\s]+)====|^([A-Za-z0-9+/=\s]+)$/;

// Big integer base-10 printing library
// Copyright (c) 2008-2024 Lapo Luchini <lapo@lapo.it>

// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
// OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

let max = 10000000000000; // biggest 10^n integer that can still fit 2^53 when multiplied by 256

class Int10 {
    /**
     * Arbitrary length base-10 value.
     * @param {number} value - Optional initial value (will be 0 otherwise).
     */
    constructor(value) {
        this.buf = [+value || 0];
    }

    /**
     * Multiply value by m and add c.
     * @param {number} m - multiplier, must be < =256
     * @param {number} c - value to add
     */
    mulAdd(m, c) {
        // assert(m <= 256)
        let b = this.buf,
            l = b.length,
            i, t;
        for (i = 0; i < l; ++i) {
            t = b[i] * m + c;
            if (t < max)
                c = 0;
            else {
                c = 0|(t / max);
                t -= c * max;
            }
            b[i] = t;
        }
        if (c > 0)
            b[i] = c;
    }

    /**
     * Convert to decimal string representation.
     * @param {*} base - optional value, only value accepted is 10
     */
    toString(base) {
        if ((base || 10) != 10)
            throw 'only base 10 is supported';
        let b = this.buf,
            s = b[b.length - 1].toString();
        for (let i = b.length - 2; i >= 0; --i)
            s += (max + b[i]).toString().substring(1);
        return s;
    }

    /**
     * Return value as a simple Number (if it is <= 10000000000000), or return this.
     */
    simplify() {
        let b = this.buf;
        return (b.length == 1) ? b[0] : this;
    }

}



// ASN.1 JavaScript decoder
// Copyright (c) 2008-2024 Lapo Luchini <lapo@lapo.it>

// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
// OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

let oids = {};
const
    ellipsis = '\u2026',
    reTimeS =     /^(\d\d)(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])([01]\d|2[0-3])(?:([0-5]\d)(?:([0-5]\d)(?:[.,](\d{1,3}))?)?)?(Z|(-(?:0\d|1[0-2])|[+](?:0\d|1[0-4]))([0-5]\d)?)?$/,
    reTimeL = /^(\d\d\d\d)(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])([01]\d|2[0-3])(?:([0-5]\d)(?:([0-5]\d)(?:[.,](\d{1,3}))?)?)?(Z|(-(?:0\d|1[0-2])|[+](?:0\d|1[0-4]))([0-5]\d)?)?$/;

function stringCut(str, len) {
    if (str.length > len)
        str = str.substring(0, len) + ellipsis;
    return str;
}

/** Class to manage a stream of bytes, with a zero-copy approach.
 * It uses an existing array or binary string and advances a position index. */
class Stream {

    /**
     * @param {Stream|array|string} enc data (will not be copied)
     * @param {?number} pos starting position (mandatory when `end` is not a Stream)
     */
    constructor(enc, pos) {
        if (enc instanceof Stream) {
            this.enc = enc.enc;
            this.pos = enc.pos;
        } else {
            this.enc = enc;
            this.pos = pos;
        }
        if (typeof this.pos != 'number')
            throw new Error('"pos" must be a numeric value');
        if (typeof this.enc == 'string')
            this.getRaw = pos => this.enc.charCodeAt(pos);
        else if (typeof this.enc[0] == 'number')
            this.getRaw = pos => this.enc[pos];
        else
            throw new Error('"enc" must be a numeric array or a string');
    }
    /** Get the byte at current position (and increment it) or at a specified position (and avoid moving current position).
     * @param {?number} pos read position if specified, else current position (and increment it) */
    get(pos) {
        if (pos === undefined)
            pos = this.pos++;
        if (pos >= this.enc.length)
            throw new Error('Requesting byte offset ' + pos + ' on a stream of length ' + this.enc.length);
        return this.getRaw(pos);
    }
    parseStringISO(start, end, maxLength) {
        let s = '';
        for (let i = start; i < end; ++i)
            s += String.fromCharCode(this.get(i));
        return { size: s.length, str: stringCut(s, maxLength) };
    }
    parseTime(start, end, shortYear) {
        let s = this.parseStringISO(start, end).str,
            m = (shortYear ? reTimeS : reTimeL).exec(s);
        if (!m)
            throw new Error('Unrecognized time: ' + s);
        if (shortYear) {
            // to avoid querying the timer, use the fixed range [1970, 2069]
            // it will conform with ITU X.400 [-10, +40] sliding window until 2030
            m[1] = +m[1];
            m[1] += (m[1] < 70) ? 2000 : 1900;
        }
        s = m[1] + '-' + m[2] + '-' + m[3] + ' ' + m[4];
        if (m[5]) {
            s += ':' + m[5];
            if (m[6]) {
                s += ':' + m[6];
                if (m[7])
                    s += '.' + m[7];
            }
        }
        if (m[8]) {
            s += ' UTC';
            if (m[9])
                s += m[9] + ':' + (m[10] || '00');
        }
        return s;
    }
}

class ASN1Tag {
    constructor(stream) {
        let buf = stream.get();
        this.tagClass = buf >> 6;
        this.tagConstructed = ((buf & 0x20) !== 0);
        this.tagNumber = buf & 0x1F;
        if (this.tagNumber == 0x1F) { // long tag
            let n = new Int10();
            do {
                buf = stream.get();
                n.mulAdd(128, buf & 0x7F);
            } while (buf & 0x80);
            this.tagNumber = n.simplify();
        }
    }
    isUniversal() {
        return this.tagClass === 0x00;
    }
    isEOC() {
        return this.tagClass === 0x00 && this.tagNumber === 0x00;
    }
}

class ASN1 {
    constructor(stream, header, length, tag, tagLen, sub) {
        if (!(tag instanceof ASN1Tag)) throw new Error('Invalid tag value.');
        this.stream = stream;
        this.header = header;
        this.length = length;
        this.tag = tag;
        this.tagLen = tagLen;
        this.sub = sub;
    }
    typeName() {
        switch (this.tag.tagClass) {
        case 0: // universal
            switch (this.tag.tagNumber) {
            case 0x00: return 'EOC';
            case 0x01: return 'BOOLEAN';
            case 0x02: return 'INTEGER';
            case 0x03: return 'BIT_STRING';
            case 0x04: return 'OCTET_STRING';
            case 0x05: return 'NULL';
            case 0x06: return 'OBJECT_IDENTIFIER';
            case 0x07: return 'ObjectDescriptor';
            case 0x08: return 'EXTERNAL';
            case 0x09: return 'REAL';
            case 0x0A: return 'ENUMERATED';
            case 0x0B: return 'EMBEDDED_PDV';
            case 0x0C: return 'UTF8String';
            case 0x0D: return 'RELATIVE_OID';
            case 0x10: return 'SEQUENCE';
            case 0x11: return 'SET';
            case 0x12: return 'NumericString';
            case 0x13: return 'PrintableString'; // ASCII subset
            case 0x14: return 'TeletexString'; // aka T61String
            case 0x15: return 'VideotexString';
            case 0x16: return 'IA5String'; // ASCII
            case 0x17: return 'UTCTime';
            case 0x18: return 'GeneralizedTime';
            case 0x19: return 'GraphicString';
            case 0x1A: return 'VisibleString'; // ASCII subset
            case 0x1B: return 'GeneralString';
            case 0x1C: return 'UniversalString';
            case 0x1E: return 'BMPString';
            }
            return 'Universal_' + this.tag.tagNumber.toString();
        case 1: return 'Application_' + this.tag.tagNumber.toString();
        case 2: return '[' + this.tag.tagNumber.toString() + ']'; // Context
        case 3: return 'Private_' + this.tag.tagNumber.toString();
        }
    }
    /** A string preview of the content (intended for humans). */
    content(maxLength) {
        if (this.tag === undefined)
            return null;
        if (maxLength === undefined)
            maxLength = Infinity;
        let content = this.posContent(),
            len = Math.abs(this.length);
        if (!this.tag.isUniversal()) {
            return "non-universal tag"
        }
        switch (this.tag.tagNumber) {
        case 0x17: // UTCTime
        case 0x18: // GeneralizedTime
            return this.stream.parseTime(content, content + len, (this.tag.tagNumber == 0x17));
        }
        return null;
    }
    posStart() {
        return this.stream.pos;
    }
    posContent() {
        return this.stream.pos + this.header;
    }
    posEnd() {
        return this.stream.pos + this.header + Math.abs(this.length);
    }
    /** Position of the length. */
    posLen() {
        return this.stream.pos + this.tagLen;
    }
    static decodeLength(stream) {
        let buf = stream.get(),
            len = buf & 0x7F;
        if (len == buf) // first bit was 0, short form
            return len;
        if (len === 0) // long form with length 0 is a special case
            return null; // undefined length
        if (len > 6) // no reason to use Int10, as it would be a huge buffer anyways
            throw new Error('Length over 48 bits not supported at position ' + (stream.pos - 1));
        buf = 0;
        for (let i = 0; i < len; ++i)
            buf = (buf * 256) + stream.get();
        return buf;
    }
    static decode(stream, offset, type = ASN1) {
        if (!(type == ASN1 || type.prototype instanceof ASN1))
            throw new Error('Must pass a class that extends ASN1');
        if (!(stream instanceof Stream))
            stream = new Stream(stream, offset || 0);
        let streamStart = new Stream(stream),
            tag = new ASN1Tag(stream),
            tagLen = stream.pos - streamStart.pos,
            len = ASN1.decodeLength(stream),
            start = stream.pos,
            header = start - streamStart.pos,
            sub = null,
            getSub = function () {
                sub = [];
                if (len !== null) {
                    // definite length
                    let end = start + len;
                    if (end > stream.enc.length)
                        throw new Error('Container at offset ' + start +  ' has a length of ' + len + ', which is past the end of the stream');
                    while (stream.pos < end)
                        sub[sub.length] = type.decode(stream);
                    if (stream.pos != end)
                        throw new Error('Content size is not correct for container at offset ' + start);
                } else {
                    // undefined length
                    try {
                        for (;;) {
                            let s = type.decode(stream);
                            if (s.tag.isEOC())
                                break;
                            sub[sub.length] = s;
                        }
                        len = start - stream.pos; // undefined lengths are represented as negative values
                    } catch (e) {
                        throw new Error('Exception while decoding undefined length content at offset ' + start + ': ' + e);
                    }
                }
            };
        if (tag.tagConstructed) {
            // must have valid content
            getSub();
        } else if (tag.isUniversal() && ((tag.tagNumber == 0x03) || (tag.tagNumber == 0x04))) {
            // sometimes BitString and OctetString are used to encapsulate ASN.1
            try {
                if (tag.tagNumber == 0x03)
                    if (stream.get() != 0)
                        throw new Error('BIT STRINGs with unused bits cannot encapsulate.');
                getSub();
                for (let s of sub) {
                    if (s.tag.isEOC())
                        throw new Error('EOC is not supposed to be actual content.');
                    try {
                        s.content();
                    } catch (e) {
                        throw new Error('Unable to parse content: ' + e);
                    }
                }
            } catch (e) {
                // but silently ignore when they don't
                sub = null;
                //DEBUG console.log('Could not decode structure at ' + start + ':', e);
            }
        }
        if (sub === null) {
            if (len === null)
                throw new Error("We can't skip over an invalid tag with undefined length at offset " + start);
            stream.pos = start + Math.abs(len);
        }
        return new type(streamStart, header, len, tag, tagLen, sub);
    }

}

let derBytes = Base64.unarmor(pem);
let certificate = ASN1.decode(derBytes);
let notBefore = certificate.sub[0].sub[4].sub[0].content();
let notAfter = certificate.sub[0].sub[4].sub[1].content();
return {"notAfter": notAfter, "notBefore": notBefore};
""";


FROM `_ANALYSIS_PROJECT_ID._BQ_DATASET_._BQ_TABLE_`
|> where asset_type = "container.googleapis.com/Cluster"
|> extend parse_json(resource.data) as resource_data
|> extend json_value(resource_data.masterAuth.clusterCaCertificate) as ca_certificates_field
|> extend cast(from_base64(ca_certificates_field) as string) as ca_certificates_pem
|> cross join unnest(split(ca_certificates_pem, "-----BEGIN")) as mangled_single_pem
|> where mangled_single_pem != ""
|> extend concat("-----BEGIN", mangled_single_pem) as single_pem
|> extend certDecode(single_pem) as cert_info
|> select name, json_value(cert_info.notBefore) as not_before, json_value(cert_info.notAfter) as not_after


