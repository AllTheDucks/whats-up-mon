package ducksmon

import (
    "fmt"
    "net/http"
    "strings"
    "io/ioutil"
    "appengine"
    "appengine/urlfetch"
    "appengine/datastore"
    "text/template"
)

var indexTemplate = template.Must(template.ParseFiles("templates/index.html"))

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

func init() {
    http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

    http.HandleFunc("/", indexHandler)
    http.HandleFunc("/add", addHandler)
    http.HandleFunc("/check", checkHandler)
    http.HandleFunc("/enable", enableHandler)
    http.HandleFunc("/disable", disableHandler)
    http.HandleFunc("/delete", deleteHandler)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
    
    services, err := getServices(c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return;
	}

    if err := indexTemplate.Execute(w, services); err != nil {
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
    client := urlfetch.Client(c)
    
    serviceRecords, err := getServices(c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return;
	}

	for _, serviceRecord := range serviceRecords {
		service := serviceRecord.Service

		if !service.Enabled {
			continue
		}

		err := validate(client, service.Url, service.HealthString)
	    if err != nil {
	    	fmt.Fprint(w, err)

	    	if(service.Up) {
	    		//notify
	    		service.Up = false;
	    		datastore.Put(c, serviceRecord.Key, &service)
	    	}
	    } else if(!service.Up) {
    		//notify
    		service.Up = true;
    		datastore.Put(c, serviceRecord.Key, &service)
	    }
	}

    fmt.Fprint(w, "Done!");
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