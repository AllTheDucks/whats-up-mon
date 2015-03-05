package whatsupmon

import (
    "fmt"
    "net/http"
    "strings"
    "io/ioutil"
    "text/template"
    "bytes"
    "time"

    "appengine"
    "appengine/urlfetch"
    "appengine/datastore"
    "appengine/mail"
)

var indexTemplate = template.Must(template.ParseFiles("templates/index.html"))
var notifSubjectTempl = template.Must(template.ParseFiles("templates/notifsubject.txt"))
var notifBodyTempl = template.Must(template.ParseFiles("templates/notifbody.txt"))


type Service struct {
	Url string
	HealthString string
	Up bool
	Enabled bool
}

type ServiceRecord struct {
	Service Service
	Key *datastore.Key
}

type Address struct {
	Email string
}

type AddressRecord struct {
	Address Address
	Key *datastore.Key
}

type Notification struct {
	Up []Service
	Down []Service
}

type IndexModel struct {
	ServiceRecords []ServiceRecord
	AddressRecords []AddressRecord
}

func init() {
    http.HandleFunc("/", indexHandler)
    http.HandleFunc("/add", addHandler)
    http.HandleFunc("/delete", deleteHandler)
    http.HandleFunc("/check", checkHandler)
    http.HandleFunc("/enable", enableHandler)
    http.HandleFunc("/disable", disableHandler)
    http.HandleFunc("/addaddr", addAddrHandler)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
    
    services, err := getServices(c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return;
	}

	addresses, err := getAddresses(c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return;
	}

	model := IndexModel{
		ServiceRecords: services,
		AddressRecords: addresses,
	}

    if err := indexTemplate.Execute(w, model); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
    }
}

func addHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
    service := Service{
            Url: r.FormValue("url"),
            HealthString: r.FormValue("healthstring"),
            Up: false,
            Enabled: true,
    }

    key := datastore.NewIncompleteKey(c, "service", serviceKey(c))
    _, err := datastore.Put(c, key, &service)
    if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
    }
    http.Redirect(w, r, "/", http.StatusFound)
}

func deleteHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
    key, err := datastore.DecodeKey(r.FormValue("key"));
    if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return;
	}

    datastore.Delete(c, key);

    http.Redirect(w, r, "/", http.StatusFound)
}

func enableHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
    key, err := datastore.DecodeKey(r.FormValue("key"));
    if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return;
	}

	err = setServiceEnabled(c, key, true);
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return;
	}

    http.Redirect(w, r, "/", http.StatusFound)
}

func disableHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
    key, err := datastore.DecodeKey(r.FormValue("key"));
    if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return;
	}

	err = setServiceEnabled(c, key, false);
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return;
	}

    http.Redirect(w, r, "/", http.StatusFound)
}

func setServiceEnabled(c appengine.Context, key *datastore.Key, enabled bool) error {
	var service Service;
	err := datastore.Get(c, key, &service)
	if err != nil {
		return err;
	}

	service.Enabled = enabled
    _, err = datastore.Put(c, key, &service)

    return err
}

func checkHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	timeout, _:= time.ParseDuration("20s");
	transport := &urlfetch.Transport{
		Context: c,
		Deadline: timeout,
		AllowInvalidServerCertificate: false,
	}
    client := &http.Client{
    	Transport: transport,
    	Timeout: 0,
    }
    
    serviceRecords, err := getServices(c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return;
	}

	var notification Notification

	for _, serviceRecord := range serviceRecords {
		service := serviceRecord.Service

		if !service.Enabled {
			continue
		}

		err := validate(client, service.Url, service.HealthString)
	    if err != nil {
	    	c.Infof("%v", err)

	    	if(service.Up) {
	    		service.Up = false;
	    		datastore.Put(c, serviceRecord.Key, &service)
	    		notification.Down = append(notification.Down, service)
	    	}
	    } else if(!service.Up) {
    		service.Up = true;
    		datastore.Put(c, serviceRecord.Key, &service)
    		notification.Up = append(notification.Up, service)
	    }
	}

	if len(notification.Up) > 0 || len(notification.Down) > 0 {
		if err := notify(c, notification); err != nil {
    		c.Errorf("Couldn't send notification: %v", err)
    	}
	}

    c.Infof("%s", "Completed Checks.");
}

func validate(client *http.Client, url string, healthstring string) error {
    resp, err := client.Get(url)
	if err != nil {	
		return fmt.Errorf("Could not access %s: %v", url, err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Could not read the body of the response for %s: %v", url, err)
	}

	if !strings.Contains(string(body), healthstring) {
		return fmt.Errorf("Could not find health string on %s", url)
	}

	return nil
}

func serviceKey(c appengine.Context) *datastore.Key {
        return datastore.NewKey(c, "service", "services", 0, nil)
}

func getServices(c appengine.Context) ([]ServiceRecord, error) {
	q := datastore.NewQuery("service").Ancestor(serviceKey(c))

    var services []Service
    keys, err := q.GetAll(c, &services)
    if err != nil {
            return nil, err
    }

	var serviceRecords = make([]ServiceRecord, 0, len(services))

	for index, service := range services {
		sr := ServiceRecord{
			Service: service,
			Key: keys[index],
		}
		serviceRecords = append(serviceRecords, sr)
	}

    return serviceRecords, nil
}

func notify(c appengine.Context, notification Notification) error {
	var subjectBuf bytes.Buffer
	if err := notifSubjectTempl.Execute(&subjectBuf, notification); err != nil {
		return err
	}
	var bodyBuf bytes.Buffer
	if err := notifBodyTempl.Execute(&bodyBuf, notification); err != nil {
		return err
	}

	addrRecords, err := getAddresses(c)
	if err != nil {
		return err
	}
	var toAddrs = make([]string, 0, len(addrRecords))
	for _, address := range addrRecords {
		toAddrs = append(toAddrs, address.Address.Email)
	}

	msg := &mail.Message{
        Sender:  "What's Up Mon? <notify@whats-up-mon.appspotmail.com>",
        To:      toAddrs,
        Subject: subjectBuf.String(),
        Body:    bodyBuf.String(),
    }
    if err := mail.Send(c, msg); err != nil {
        return err
    }

    return nil
}

func addAddrHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
    addr := Address {
    	Email: r.FormValue("addr"),
    }

    key := datastore.NewIncompleteKey(c, "address", addressKey(c))
    _, err := datastore.Put(c, key, &addr)
    if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
    }
    http.Redirect(w, r, "/", http.StatusFound)
}

func addressKey(c appengine.Context) *datastore.Key {
        return datastore.NewKey(c, "address", "addresses", 0, nil)
}

func getAddresses(c appengine.Context) ([]AddressRecord, error) {
	q := datastore.NewQuery("address").Ancestor(addressKey(c))

    var addresses []Address
    keys, err := q.GetAll(c, &addresses)
    if err != nil {
    	return nil, err
    }

	var addressRecords = make([]AddressRecord, 0, len(addresses))

	for index, address := range addresses {
		addr := AddressRecord{
			Address: address,
			Key: keys[index],
		}
		addressRecords = append(addressRecords, addr)
	}

    return addressRecords, nil
}